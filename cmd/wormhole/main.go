package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"github.com/wormhole-dev/wormhole/internal/client"
	"github.com/wormhole-dev/wormhole/internal/inspect"
	"github.com/wormhole-dev/wormhole/pkg/config"
)

var version = "dev"

func main() {
	rootCmd := &cobra.Command{
		Use:   "wormhole",
		Short: "Expose local servers to the internet",
		Long:  "Wormhole gives your local server a public URL instantly.",
	}

	rootCmd.AddCommand(httpCmd())
	rootCmd.AddCommand(loginCmd())
	rootCmd.AddCommand(logoutCmd())
	rootCmd.AddCommand(statusCmd())
	rootCmd.AddCommand(versionCmd())
	rootCmd.AddCommand(uninstallCmd())
	rootCmd.AddCommand(updateCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func httpCmd() *cobra.Command {
	var headless bool
	var subdomain string
	var inspectAddr string
	var noInspect bool

	cmd := &cobra.Command{
		Use:   "http [port]",
		Short: "Expose a local HTTP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			port := args[0]
			localAddr := fmt.Sprintf("localhost:%s", port)

			logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).
				With().Timestamp().Logger().Level(zerolog.InfoLevel)

			// Create context that cancels on Ctrl+C
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				logger.Info().Msg("shutting down")
				cancel()
			}()

			// Load auth token if custom subdomain requested
			var token string
			if subdomain != "" {
				cfg, err := config.Load()
				if err != nil {
					return fmt.Errorf("loading config: %w", err)
				}
				if cfg.Token == "" {
					return fmt.Errorf("custom subdomains require authentication. Run 'wormhole login' first")
				}
				token = cfg.Token
			}

			// Create client
			c := client.New(client.Config{
				RelayURL:  config.DefaultRelayURL,
				LocalAddr: localAddr,
				Subdomain: subdomain,
				Token:     token,
				Logger:    logger,
			})

			// Start inspector server
			var inspSrv *inspect.Server
			if !noInspect {
				inspSrv = inspect.NewServer(c.Recorder(), localAddr, logger)
				if err := inspSrv.Start(inspectAddr); err != nil {
					logger.Warn().Err(err).Msg("failed to start inspector")
				} else {
					defer inspSrv.Close()
				}
			}

			if headless {
				if inspSrv != nil {
					fmt.Fprintf(os.Stdout, "Inspector: http://%s\n", inspSrv.Addr())
				}
				return runHeadless(ctx, c, logger)
			}
			return runTUI(ctx, c, localAddr, logger, inspSrv)
		},
	}

	cmd.Flags().BoolVar(&headless, "headless", false, "Run without terminal UI (plain log output)")
	cmd.Flags().StringVar(&subdomain, "subdomain", "", "Request a custom subdomain (e.g. myapp)")
	cmd.Flags().StringVar(&inspectAddr, "inspect", "localhost:4040", "Inspector dashboard address")
	cmd.Flags().BoolVar(&noInspect, "no-inspect", false, "Disable the traffic inspector")

	return cmd
}

func runHeadless(ctx context.Context, c *client.Client, logger zerolog.Logger) error {
	c.OnStatus(func(status string) {
		logger.Info().Str("status", status).Msg("status changed")
	})
	c.OnRequest(func(r client.RequestLog) {
		logger.Info().
			Str("method", r.Method).
			Str("path", r.Path).
			Int("status", r.Status).
			Dur("latency", r.Latency).
			Msg("request")
	})

	// Print tunnel URL once connected
	go func() {
		for ctx.Err() == nil {
			if t := c.Tunnel(); t != nil {
				fmt.Fprintf(os.Stdout, "\nForwarding: %s -> http://%s\n\n", t.URL, c.LocalAddr())
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	return c.Run(ctx)
}

func runTUI(ctx context.Context, c *client.Client, localAddr string, logger zerolog.Logger, inspSrv *inspect.Server) error {
	var inspAddr string
	if inspSrv != nil {
		inspAddr = inspSrv.Addr()
	}
	m := client.NewModel(localAddr, inspAddr)
	p := tea.NewProgram(m)

	c.OnStatus(func(status string) {
		p.Send(client.StatusMsg(status))
	})
	c.OnRequest(func(r client.RequestLog) {
		p.Send(client.RequestMsg(r))
	})

	go func() {
		if err := c.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error().Err(err).Msg("client error")
		}
		p.Quit()
	}()

	go func() {
		for ctx.Err() == nil {
			if t := c.Tunnel(); t != nil {
				p.Send(client.TunnelMsg(*t))
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("UI error: %w", err)
	}
	return nil
}

func loginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Authenticate with GitHub to use custom subdomains",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Start local HTTP server on random port
			listener, err := net.Listen("tcp", "localhost:0")
			if err != nil {
				return fmt.Errorf("starting local server: %w", err)
			}
			port := listener.Addr().(*net.TCPAddr).Port

			resultCh := make(chan struct {
				token    string
				username string
				err      error
			}, 1)

			mux := http.NewServeMux()
			mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
				token := r.URL.Query().Get("token")
				username := r.URL.Query().Get("username")
				if token == "" {
					resultCh <- struct {
						token    string
						username string
						err      error
					}{err: fmt.Errorf("no token received")}
					http.Error(w, "Authentication failed", http.StatusBadRequest)
					return
				}

				resultCh <- struct {
					token    string
					username string
					err      error
				}{token: token, username: username}

				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<!DOCTYPE html><html><body style="font-family:system-ui;text-align:center;padding:4rem">
					<h2>Authenticated!</h2>
					<p>You can close this window and return to the terminal.</p>
				</body></html>`)
			})

			server := &http.Server{Handler: mux}
			go server.Serve(listener)
			defer server.Close()

			// Open browser — go directly to GitHub, skip edge redirect
			state := fmt.Sprintf("%d", port)
			authURL := fmt.Sprintf(
				"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=read:user&state=%s",
				config.GitHubClientID,
				config.CallbackURL,
				state,
			)
			fmt.Printf("Opening browser to authenticate with GitHub...\n")
			fmt.Printf("If the browser doesn't open, visit: %s\n\n", authURL)
			openBrowser(authURL)

			// Wait for callback (with timeout)
			select {
			case result := <-resultCh:
				if result.err != nil {
					return fmt.Errorf("authentication failed: %w", result.err)
				}

				// Save to config
				cfg, err := config.Load()
				if err != nil {
					cfg = &config.UserConfig{}
				}
				cfg.Token = result.token
				cfg.Username = result.username
				if err := cfg.Save(); err != nil {
					return fmt.Errorf("saving config: %w", err)
				}

				fmt.Printf("Logged in as %s\n", result.username)
				return nil

			case <-time.After(2 * time.Minute):
				return fmt.Errorf("authentication timed out — no response received within 2 minutes")
			}
		},
	}
}

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored authentication",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if cfg.Token == "" {
				fmt.Println("Not logged in.")
				return nil
			}
			username := cfg.Username
			cfg.Token = ""
			cfg.Username = ""
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
			fmt.Printf("Logged out %s\n", username)
			return nil
		},
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	cmd.Start()
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current authentication status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if cfg.Token == "" {
				fmt.Println("Not logged in.")
				fmt.Println("Run 'wormhole login' to authenticate with GitHub.")
				return nil
			}
			fmt.Printf("Logged in as: %s\n", cfg.Username)
			return nil
		},
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("wormhole %s\n", version)
		},
	}
}

func isBrewInstall() bool {
	binPath, err := os.Executable()
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(binPath)
	if err != nil {
		return false
	}
	return strings.Contains(resolved, "Cellar") || strings.Contains(resolved, "homebrew")
}

func uninstallCmd() *cobra.Command {
	var purge bool

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove wormhole from your system",
		RunE: func(cmd *cobra.Command, args []string) error {
			brewInstall := isBrewInstall()

			if brewInstall {
				fmt.Println("Installed via: Homebrew")
				fmt.Println("Formula:       MuhammadHananAsghar/tap/wormhole")
			} else {
				binPath, err := os.Executable()
				if err != nil {
					return fmt.Errorf("finding wormhole binary: %w", err)
				}
				binPath, err = filepath.EvalSymlinks(binPath)
				if err != nil {
					return fmt.Errorf("resolving binary path: %w", err)
				}
				fmt.Printf("Binary: %s\n", binPath)
			}

			if purge {
				configDir, _ := config.ConfigDir()
				if configDir != "" {
					fmt.Printf("Config: %s\n", configDir)
				}
			}

			fmt.Print("\nRemove wormhole? [y/N] ")
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" {
				fmt.Println("Cancelled.")
				return nil
			}

			if brewInstall {
				// Use brew uninstall for Homebrew installations
				fmt.Println("Running: brew uninstall wormhole")
				brewCmd := exec.Command("brew", "uninstall", "wormhole")
				brewCmd.Stdin = os.Stdin
				brewCmd.Stdout = os.Stdout
				brewCmd.Stderr = os.Stderr
				if err := brewCmd.Run(); err != nil {
					return fmt.Errorf("brew uninstall failed: %w", err)
				}
			} else {
				// Manual removal for curl/go install
				binPath, _ := os.Executable()
				binPath, _ = filepath.EvalSymlinks(binPath)

				if err := os.Remove(binPath); err != nil {
					if os.IsPermission(err) {
						fmt.Println("Permission denied. Trying with sudo...")
						sudoCmd := exec.Command("sudo", "rm", binPath)
						sudoCmd.Stdin = os.Stdin
						sudoCmd.Stdout = os.Stdout
						sudoCmd.Stderr = os.Stderr
						if err := sudoCmd.Run(); err != nil {
							return fmt.Errorf("removing binary with sudo: %w", err)
						}
					} else {
						return fmt.Errorf("removing binary: %w", err)
					}
				}
				fmt.Printf("Removed %s\n", binPath)
			}

			// Remove config directory if --purge
			if purge {
				configDir, err := config.ConfigDir()
				if err == nil {
					if err := os.RemoveAll(configDir); err != nil {
						return fmt.Errorf("removing config directory: %w", err)
					}
					fmt.Printf("Removed %s\n", configDir)
				}
			}

			fmt.Println("\nwormhole has been uninstalled.")
			return nil
		},
	}

	cmd.Flags().BoolVar(&purge, "purge", false, "Also remove config directory (~/.wormhole/)")

	return cmd
}

// ── update command ────────────────────────────────────────────────────────────

func updateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update wormhole to the latest version",
		Long: `Check for the latest version and update the wormhole binary in place.
Detects the install method (Homebrew, curl, go install) and uses the appropriate update path.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate()
		},
	}
}

func runUpdate() error {
	fmt.Printf("wormhole %s — checking for updates...\n\n", version)

	latest, downloadURL, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("checking latest release: %w", err)
	}

	if latest == version {
		fmt.Printf("  Already up to date (v%s).\n", version)
		return nil
	}

	fmt.Printf("  New version available: %s → %s\n\n", version, latest)

	// 1. Homebrew?
	if brewOut, berr := exec.Command("brew", "list", "--formula").Output(); berr == nil {
		if strings.Contains(string(brewOut), "wormhole") {
			fmt.Println("  Updating via Homebrew...")
			cmd := exec.Command("brew", "upgrade", "MuhammadHananAsghar/tap/wormhole")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("brew upgrade failed: %w", err)
			}
			fmt.Printf("\n  Updated to %s via Homebrew.\n", latest)
			return nil
		}
	}

	// 2. go install?
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		gobin := filepath.Join(gopath, "bin", "wormhole")
		if self, _ := os.Executable(); self == gobin {
			fmt.Println("  Updating via go install...")
			cmd := exec.Command("go", "install", "github.com/MuhammadHananAsghar/wormhole/cmd/wormhole@latest")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("go install failed: %w", err)
			}
			fmt.Printf("\n  Updated to %s via go install.\n", latest)
			return nil
		}
	}
	if home, _ := os.UserHomeDir(); home != "" {
		gobin := filepath.Join(home, "go", "bin", "wormhole")
		if self, _ := os.Executable(); self == gobin {
			fmt.Println("  Updating via go install...")
			cmd := exec.Command("go", "install", "github.com/MuhammadHananAsghar/wormhole/cmd/wormhole@latest")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("go install failed: %w", err)
			}
			fmt.Printf("\n  Updated to %s via go install.\n", latest)
			return nil
		}
	}

	// 3. Direct binary replacement.
	if downloadURL == "" {
		return fmt.Errorf("no compatible binary found for %s/%s in the latest release", runtime.GOOS, runtime.GOARCH)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine current binary path: %w", err)
	}

	fmt.Printf("  Downloading %s...\n", downloadURL)

	tmpDir, err := os.MkdirTemp("", "wormhole-update-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, "wormhole.tar.gz")
	if err := downloadFile(downloadURL, archivePath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	extractCmd := exec.Command("tar", "-xzf", archivePath, "-C", tmpDir)
	if out, err := extractCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extraction failed: %s", strings.TrimSpace(string(out)))
	}

	newBinary := filepath.Join(tmpDir, "wormhole")
	if _, err := os.Stat(newBinary); err != nil {
		return fmt.Errorf("extracted binary not found")
	}

	fmt.Printf("  Replacing %s...\n", self)
	if err := replaceBinary(self, newBinary); err != nil {
		return fmt.Errorf("replacing binary: %w", err)
	}

	fmt.Printf("\n  Updated to %s.\n", latest)
	return nil
}

func fetchLatestRelease() (string, string, error) {
	apiURL := "https://api.github.com/repos/MuhammadHananAsghar/wormhole/releases/latest"
	resp, err := http.Get(apiURL)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", err
	}

	ver := strings.TrimPrefix(release.TagName, "v")

	archiveName := fmt.Sprintf("wormhole_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	var downloadURL string
	for _, a := range release.Assets {
		if a.Name == archiveName {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}

	return ver, downloadURL, nil
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

func replaceBinary(oldPath, newPath string) error {
	if err := os.Chmod(newPath, 0o755); err != nil {
		return err
	}

	if err := os.Rename(newPath, oldPath); err == nil {
		return nil
	}

	src, err := os.Open(newPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(oldPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		// Need sudo.
		cmd := exec.Command("sudo", "cp", newPath, oldPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}
