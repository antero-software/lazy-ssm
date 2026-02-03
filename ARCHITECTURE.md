# Architecture Documentation

This document describes the architecture and organization of the lazy-ssm project.

## Project Structure

```
lazy-ssm/
├── main.go                 # Application entry point (minimal)
├── cmd/                    # Command-line interface commands
│   ├── root.go            # Root command and main tunnel manager logic
│   ├── start.go           # Start daemon command
│   ├── stop.go            # Stop daemon command
│   ├── restart.go         # Restart daemon command
│   ├── reload.go          # Reload configuration command
│   └── status.go          # Status check command
├── config/                 # Configuration management
│   ├── config.go          # Configuration structures and loading
│   └── validation.go      # Configuration validation
├── daemon/                 # Daemon process management
│   └── daemon.go          # PID files, Unix sockets, IPC
├── tunnel/                 # Tunnel management
│   ├── manager.go         # TunnelManager for multiple tunnels
│   └── tunnel.go          # LazySSMTunnel implementation
├── ec2/                    # AWS EC2 instance discovery
│   └── discovery.go       # Instance pattern matching
└── sso/                    # AWS SSO authentication
    └── auth.go            # SSO profile checking
```

## Package Responsibilities

### main
- Entry point for the application
- Delegates to cmd package
- Minimal code for clean separation

### cmd
- Command-line interface implementation using Cobra
- Each command in its own file for clarity
- Shares common flags and configuration through root.go

**Files:**
- `root.go`: Root command, persistent flags, main tunnel manager logic
- `start.go`: Start daemon in background or foreground
- `stop.go`: Graceful shutdown with fallback to force kill
- `restart.go`: Stop and start sequence
- `reload.go`: Hot reload configuration
- `status.go`: Display daemon and tunnel status

### daemon
- Process lifecycle management
- PID file handling (`/tmp/lazy-ssm.pid`)
- Unix socket IPC (`/tmp/lazy-ssm.sock`)
- Command/response protocol
- Process control (signals, waiting for exit)

**Key Features:**
- Check if daemon is running
- Send commands to running daemon
- Graceful shutdown coordination
- Fallback to force kill if needed

### tunnel
- Core tunnel management logic
- On-demand tunnel creation
- Idle timeout monitoring
- Connection proxying

**manager.go:**
- Manages multiple LazySSMTunnel instances
- Reload configuration without downtime
- Graceful shutdown of all tunnels
- Status reporting

**tunnel.go:**
- Individual tunnel lifecycle
- SSM session management
- Port forwarding proxy
- Activity monitoring
- Automatic cleanup on idle

### config
- YAML configuration loading
- Default values
- Validation rules
- Environment variable support

**Configuration Hierarchy:**
1. Default values
2. Configuration file
3. Environment variables
4. Command-line flags

### ec2
- EC2 instance discovery
- Pattern matching (e.g., `bastion-*`)
- SSM capability verification
- AWS CLI integration

### sso
- AWS SSO authentication
- Profile verification
- Session validation
- Automatic SSO prompt if needed

## Command Flow

### Start Command Flow
```
lazy-ssm start
  → Check if already running (PID file)
  → Fork background process (if --daemon=true)
  → Write PID file
  → Create Unix socket
  → Load configuration
  → Create TunnelManager
  → Register command handlers (reload, status, stop, ping)
  → Start all tunnels
  → Wait for shutdown signal
```

### Stop Command Flow
```
lazy-ssm stop
  → Check if running (PID file)
  → Send stop command via Unix socket
  → Wait for graceful shutdown (10s timeout)
  → Fallback to SIGTERM
  → Fallback to SIGKILL if timeout
  → Clean up PID file
```

### Reload Command Flow
```
lazy-ssm reload
  → Send reload command via Unix socket
  → Daemon receives command
  → Load new configuration
  → Stop existing tunnels gracefully
  → Create new tunnels
  → Start new tunnels
  → Return success/failure
```

### Status Command Flow
```
lazy-ssm status
  → Check if running (PID file)
  → Send status command via Unix socket
  → Receive tunnel status data
  → Display formatted output
```

## Tunnel Lifecycle

### Tunnel Creation
1. Listen on local port (e.g., 5432)
2. Wait for incoming connection
3. Discover EC2 instance (if using pattern)
4. Check AWS SSO authentication
5. Start SSM port forwarding session
6. Find free port for SSM tunnel
7. Wait for tunnel to be ready
8. Proxy connection bidirectionally

### Tunnel Shutdown
1. Close listener (no new connections)
2. Wait for active connections to finish (with timeout)
3. Kill SSM session process
4. Clean up resources

### Idle Monitoring
1. Track last activity timestamp
2. Count active connections
3. Check periodically (every 30s by default)
4. Close tunnel if idle for configured timeout (5m by default)
5. Tunnel automatically restarts on next connection

## IPC Protocol

### Unix Socket Communication
- Socket path: `/tmp/lazy-ssm.sock`
- Protocol: Simple text command → JSON response

**Commands:**
```go
type Command string

const (
    CmdReload  Command = "reload"
    CmdStatus  Command = "status"
    CmdStop    Command = "stop"
    CmdPing    Command = "ping"
)
```

**Response:**
```go
type Response struct {
    Success bool                   `json:"success"`
    Message string                 `json:"message"`
    Data    map[string]interface{} `json:"data,omitempty"`
}
```

### Example Interaction
```
Client → "status"
Server → {"success":true,"message":"Daemon is running","data":{"tunnel_count":2,...}}
```

## Design Patterns

### Command Pattern
- Each CLI command is a separate command object
- Encapsulates all information needed to perform action
- Supports undo (restart after stop)

### Observer Pattern
- Tunnel manager monitors tunnel states
- Idle monitor observes connection activity
- Signal handlers observe OS signals

### Proxy Pattern
- Lazy tunnel creation on first connection
- Transparent forwarding to RDS endpoints
- Automatic cleanup when idle

### Singleton Pattern
- Single daemon instance per system (via PID file)
- Single Unix socket for IPC

### Strategy Pattern
- Different reload strategies (graceful vs. force)
- Different shutdown strategies (timeout, signal)

## Error Handling

### Graceful Degradation
1. Try preferred method
2. Fall back to alternative
3. Force if necessary
4. Clean up regardless

Example (stop command):
1. Try Unix socket stop command
2. Fall back to SIGTERM
3. Force with SIGKILL
4. Clean up PID file

### Error Recovery
- Configuration reload failures don't crash daemon
- Tunnel failures don't affect other tunnels
- SSO authentication failures provide clear guidance
- Port conflicts automatically try next available port

## Concurrency Model

### Goroutines
- One per tunnel (listening loop)
- One per active connection (bidirectional proxy)
- One for idle monitoring per tunnel
- One for signal handling

### Synchronization
- Mutex for tunnel state access
- Context for graceful cancellation
- WaitGroup for coordinated shutdown
- Atomic operations for counters

### Context Usage
```
Root Context
  └─ Tunnel Manager Context
       ├─ Tunnel 1 Context
       ├─ Tunnel 2 Context
       └─ Tunnel N Context
```

Canceling the Tunnel Manager Context cascades to all tunnels.

## Configuration Reload

### Zero-Downtime Reload
The reload process ensures the daemon stays running:

1. **New Config Load**: Parse and validate new configuration
2. **Graceful Tunnel Stop**: Cancel contexts for existing tunnels
3. **Wait for Cleanup**: All tunnels finish active connections
4. **Create New Tunnels**: Instantiate new tunnel objects
5. **Start New Tunnels**: Begin listening with updated configuration

The daemon process never restarts, only the tunnel instances change.

## Future Enhancements

Potential areas for expansion:

1. **Metrics**: Prometheus/StatsD integration for tunnel metrics
2. **Health Checks**: HTTP endpoint for health monitoring
3. **Multiple Configs**: Support for multiple configuration files
4. **Log Rotation**: Built-in log rotation support
5. **Web UI**: Optional web interface for status monitoring
6. **TLS**: TLS encryption for local connections
7. **Connection Limits**: Per-tunnel connection limits
8. **Rate Limiting**: Connection rate limiting
9. **Access Control**: IP-based access control
10. **Audit Logging**: Detailed connection audit logs
