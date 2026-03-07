package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
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
