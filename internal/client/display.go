package client

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	// Brand colors
	purple    = lipgloss.Color("99")
	green     = lipgloss.Color("42")
	orange    = lipgloss.Color("208")
	red       = lipgloss.Color("196")
	yellow    = lipgloss.Color("226")
	dimWhite  = lipgloss.Color("245")
	darkGray  = lipgloss.Color("238")
	lightGray = lipgloss.Color("250")

	// Logo/header
	logoStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(purple)

	// Box for connection info
	infoBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(purple).
			Padding(0, 2).
			MarginLeft(2).
			MarginTop(1)

	// Labels and values
	labelStyle = lipgloss.NewStyle().
			Foreground(dimWhite).
			Width(14).
			Align(lipgloss.Right)

	valueStyle = lipgloss.NewStyle().
			Foreground(lightGray)

	urlHighlight = lipgloss.NewStyle().
			Bold(true).
			Foreground(green)

	localStyle = lipgloss.NewStyle().
			Foreground(dimWhite)

	arrowStyle = lipgloss.NewStyle().
			Foreground(darkGray)

	// Status indicators
	statusOnline = lipgloss.NewStyle().
			Bold(true).
			Foreground(green)

	statusConnecting = lipgloss.NewStyle().
				Bold(true).
				Foreground(orange)

	statusReconnecting = lipgloss.NewStyle().
				Bold(true).
				Foreground(yellow)

	// Request log table
	headerStyle = lipgloss.NewStyle().
			Foreground(dimWhite).
			Bold(true).
			MarginLeft(2).
			MarginTop(1)

	separatorStyle = lipgloss.NewStyle().
			Foreground(darkGray).
			MarginLeft(2)

	methodStyle = lipgloss.NewStyle().
			Bold(true).
			Width(7)

	pathStyle = lipgloss.NewStyle().
			Width(36).
			Foreground(lightGray)

	codeStyle = lipgloss.NewStyle().
			Bold(true).
			Width(6).
			Align(lipgloss.Right)

	latencyStyle = lipgloss.NewStyle().
			Width(8).
			Align(lipgloss.Right).
			Foreground(dimWhite)

	// Footer
	footerStyle = lipgloss.NewStyle().
			Foreground(darkGray).
			MarginLeft(2).
			MarginTop(1)
)

const asciiLogo = `` +
	`  █   █ █▀▀█ █▀▀█ █▀▄▀█ █  █ █▀▀█ █   █▀▀` + "\n" +
	`  █▄█▄█ █  █ █▄▄▀ █ █ █ █▀▀█ █  █ █   █▀▀` + "\n" +
	`  ▀ ▀ ▀ ▀▀▀▀ ▀ ▀▀ ▀   ▀ ▀  ▀ ▀▀▀▀ ▀▀▀ ▀▀▀`

const maxRequests = 20

// StatusMsg updates the connection status.
type StatusMsg string

// RequestMsg adds a request to the log.
type RequestMsg RequestLog

// TunnelMsg sets the tunnel info.
type TunnelMsg TunnelInfo

// Model is the bubbletea model for the CLI display.
type Model struct {
	status      string
	tunnel      *TunnelInfo
	localAddr   string
	inspectAddr string
	requests    []RequestLog
	quitting    bool
}

// NewModel creates the display model.
func NewModel(localAddr string, inspectAddr string) Model {
	return Model{
		status:      "connecting",
		localAddr:   localAddr,
		inspectAddr: inspectAddr,
		requests:    make([]RequestLog, 0, maxRequests),
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			m.quitting = true
			return m, tea.Quit
		}
	case StatusMsg:
		m.status = string(msg)
	case TunnelMsg:
		info := TunnelInfo(msg)
		m.tunnel = &info
	case RequestMsg:
		m.requests = append(m.requests, RequestLog(msg))
		if len(m.requests) > maxRequests {
			m.requests = m.requests[1:]
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.quitting {
		return "\n" + logoStyle.Render(asciiLogo) + "\n" +
			lipgloss.NewStyle().Foreground(dimWhite).MarginLeft(4).Render("Tunnel closed. Goodbye!") + "\n\n"
	}

	var b strings.Builder

	// Header — ASCII art logo
	b.WriteString("\n")
	b.WriteString(logoStyle.Render(asciiLogo))
	b.WriteString("\n")
	tagline := lipgloss.NewStyle().Foreground(darkGray).MarginLeft(4).
		Render("v0.1.0 | by Muhammad Hanan Asghar | github.com/MuhammadHananAsghar/wormhole")
	b.WriteString(tagline + "\n")

	// Connection info box
	var infoLines []string

	// Status line
	statusText := m.renderStatus()
	infoLines = append(infoLines,
		fmt.Sprintf("%s  %s", labelStyle.Render("Status"), statusText),
	)

	// Forwarding line
	if m.tunnel != nil {
		infoLines = append(infoLines,
			fmt.Sprintf("%s  %s %s %s",
				labelStyle.Render("Forwarding"),
				urlHighlight.Render(m.tunnel.URL),
				arrowStyle.Render("->"),
				localStyle.Render(fmt.Sprintf("http://%s", m.localAddr)),
			),
		)
	} else {
		infoLines = append(infoLines,
			fmt.Sprintf("%s  %s",
				labelStyle.Render("Forwarding"),
				lipgloss.NewStyle().Foreground(darkGray).Render("waiting for connection..."),
			),
		)
	}

	// Inspector line
	if m.inspectAddr != "" {
		infoLines = append(infoLines,
			fmt.Sprintf("%s  %s",
				labelStyle.Render("Inspector"),
				urlHighlight.Render(fmt.Sprintf("http://%s", m.inspectAddr)),
			),
		)
	}

	b.WriteString(infoBoxStyle.Render(strings.Join(infoLines, "\n")))
	b.WriteString("\n")

	// Request log
	if len(m.requests) > 0 {
		b.WriteString(headerStyle.Render("Requests"))
		b.WriteString("\n")
		b.WriteString(separatorStyle.Render(strings.Repeat("-", 62)))
		b.WriteString("\n")

		for _, r := range m.requests {
			b.WriteString(m.renderRequest(r))
			b.WriteString("\n")
		}
	} else if m.tunnel != nil {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(darkGray).MarginLeft(2).Render(
			"Waiting for requests..."))
		b.WriteString("\n")
	}

	// Footer
	b.WriteString(footerStyle.Render("Press q or Ctrl+C to quit"))
	b.WriteString("\n")

	return b.String()
}

func (m Model) renderStatus() string {
	dot := "●"
	switch m.status {
	case "online":
		return statusOnline.Render(dot+" connected")
	case "reconnecting":
		return statusReconnecting.Render(dot+" reconnecting...")
	default:
		return statusConnecting.Render(dot+" connecting...")
	}
}

func (m Model) renderRequest(r RequestLog) string {
	// Color-code method
	method := methodStyle
	switch r.Method {
	case "GET":
		method = method.Foreground(green)
	case "POST":
		method = method.Foreground(lipgloss.Color("39")) // blue
	case "PUT", "PATCH":
		method = method.Foreground(orange)
	case "DELETE":
		method = method.Foreground(red)
	default:
		method = method.Foreground(dimWhite)
	}

	// Color-code status
	code := codeStyle
	switch {
	case r.Status >= 500:
		code = code.Foreground(red)
	case r.Status >= 400:
		code = code.Foreground(orange)
	case r.Status >= 300:
		code = code.Foreground(yellow)
	default:
		code = code.Foreground(green)
	}

	// Format latency
	latency := fmt.Sprintf("%dms", r.Latency.Milliseconds())

	return fmt.Sprintf("  %s %s %s %s",
		method.Render(r.Method),
		pathStyle.Render(truncate(r.Path, 36)),
		code.Render(fmt.Sprintf("%d", r.Status)),
		latencyStyle.Render(latency),
	)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
