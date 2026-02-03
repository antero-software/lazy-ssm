package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const (
	defaultPidFile    = "/tmp/lazy-ssm.pid"
	defaultSocketPath = "/tmp/lazy-ssm.sock"
)

// Command represents a control command sent to the daemon
type Command string

const (
	CmdReload  Command = "reload"
	CmdStatus  Command = "status"
	CmdStop    Command = "stop"
	CmdPing    Command = "ping"
)

// Response represents a response from the daemon
type Response struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

// Daemon represents the daemon process manager
type Daemon struct {
	pidFile    string
	socketPath string
	listener   net.Listener
	handlers   map[Command]CommandHandler
}

// CommandHandler is a function that handles a daemon command
type CommandHandler func(ctx context.Context) Response

// New creates a new daemon instance
func New() *Daemon {
	return &Daemon{
		pidFile:    defaultPidFile,
		socketPath: defaultSocketPath,
		handlers:   make(map[Command]CommandHandler),
	}
}

// WithPidFile sets a custom PID file path
func (d *Daemon) WithPidFile(path string) *Daemon {
	d.pidFile = path
	return d
}

// WithSocketPath sets a custom socket path
func (d *Daemon) WithSocketPath(path string) *Daemon {
	d.socketPath = path
	return d
}

// RegisterHandler registers a command handler
func (d *Daemon) RegisterHandler(cmd Command, handler CommandHandler) {
	d.handlers[cmd] = handler
}

// Start starts the daemon and writes the PID file
func (d *Daemon) Start(ctx context.Context) error {
	// Check if daemon is already running
	if d.IsRunning() {
		pid, _ := d.GetPID()
		return fmt.Errorf("daemon already running with PID %d", pid)
	}

	// Write PID file
	if err := d.writePIDFile(); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	// Remove old socket if exists
	os.Remove(d.socketPath)

	// Start control socket
	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		d.cleanup()
		return fmt.Errorf("failed to create control socket: %w", err)
	}
	d.listener = listener

	// Start accepting commands
	go d.acceptCommands(ctx)

	return nil
}

// Stop cleans up daemon resources
func (d *Daemon) Stop() error {
	if d.listener != nil {
		d.listener.Close()
	}
	return d.cleanup()
}

// acceptCommands accepts and handles control commands
func (d *Daemon) acceptCommands(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			conn, err := d.listener.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}

			go d.handleConnection(ctx, conn)
		}
	}
}

// handleConnection handles a single control connection
func (d *Daemon) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	// Set read timeout
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	// Read command
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		d.writeResponse(conn, Response{
			Success: false,
			Message: fmt.Sprintf("failed to read command: %v", err),
		})
		return
	}

	cmd := Command(string(buf[:n]))

	// Handle command
	handler, exists := d.handlers[cmd]
	if !exists {
		d.writeResponse(conn, Response{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", cmd),
		})
		return
	}

	response := handler(ctx)
	d.writeResponse(conn, response)
}

// writeResponse writes a JSON response to the connection
func (d *Daemon) writeResponse(conn net.Conn, resp Response) {
	data, _ := json.Marshal(resp)
	conn.Write(data)
}

// SendCommand sends a command to the running daemon
func SendCommand(cmd Command) (Response, error) {
	conn, err := net.Dial("unix", defaultSocketPath)
	if err != nil {
		return Response{}, fmt.Errorf("failed to connect to daemon: %w (is the daemon running?)", err)
	}
	defer conn.Close()

	// Set timeout
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send command
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return Response{}, fmt.Errorf("failed to send command: %w", err)
	}

	// Read response
	data, err := io.ReadAll(conn)
	if err != nil {
		return Response{}, fmt.Errorf("failed to read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return Response{}, fmt.Errorf("failed to parse response: %w", err)
	}

	return resp, nil
}

// IsRunning checks if the daemon is running
func (d *Daemon) IsRunning() bool {
	pid, err := d.GetPID()
	if err != nil {
		return false
	}

	// Check if process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Try to send signal 0 to check if process is alive
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// GetPID returns the PID from the PID file
func (d *Daemon) GetPID() (int, error) {
	data, err := os.ReadFile(d.pidFile)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, fmt.Errorf("invalid PID in file: %w", err)
	}

	return pid, nil
}

// writePIDFile writes the current process PID to the PID file
func (d *Daemon) writePIDFile() error {
	// Ensure directory exists
	dir := filepath.Dir(d.pidFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	pid := os.Getpid()
	return os.WriteFile(d.pidFile, []byte(strconv.Itoa(pid)), 0644)
}

// cleanup removes PID file and socket
func (d *Daemon) cleanup() error {
	os.Remove(d.pidFile)
	os.Remove(d.socketPath)
	return nil
}

// KillProcess kills the process with the given PID
func KillProcess(pid int, signal syscall.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	if err := process.Signal(signal); err != nil {
		return fmt.Errorf("failed to send signal: %w", err)
	}

	return nil
}

// WaitForProcessExit waits for a process to exit with timeout
func WaitForProcessExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		process, err := os.FindProcess(pid)
		if err != nil {
			return nil // Process doesn't exist
		}

		// Try to send signal 0 to check if process is alive
		if err := process.Signal(syscall.Signal(0)); err != nil {
			return nil // Process is dead
		}

		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for process to exit")
}