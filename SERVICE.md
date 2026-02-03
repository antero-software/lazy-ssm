# Service Mode Documentation

Lazy SSM now supports running as a daemon service with remote control capabilities.

## Service Commands

### Start the Service

Start the tunnel manager as a background daemon:

```bash
lazy-ssm start
```

Run in foreground mode (useful for debugging):

```bash
lazy-ssm start --daemon=false
```

### Stop the Service

Gracefully stop the running daemon:

```bash
lazy-ssm stop
```

The stop command will:
1. Try graceful shutdown via control socket
2. Wait for active connections to finish (up to 10 seconds)
3. Fall back to SIGTERM if needed
4. Force kill (SIGKILL) if timeout is exceeded

### Restart the Service

Restart the daemon (stop + start):

```bash
lazy-ssm restart
```

This is equivalent to:
```bash
lazy-ssm stop && lazy-ssm start
```

### Reload Configuration

Reload configuration without stopping the service:

```bash
lazy-ssm reload
```

The reload command will:
1. Load the new configuration file
2. Stop all existing tunnels gracefully
3. Create new tunnels based on the updated configuration
4. Start the new tunnels

This allows you to update tunnel configurations without downtime for the service itself.

### Check Status

Check if the daemon is running and view tunnel status:

```bash
lazy-ssm status
```

Output example:
```
Daemon is running with PID 12345
Tunnels: 2

Active tunnels:
  - db1 (Production Database)
    Local port: 5432
    RDS endpoint: prod-db.us-east-1.rds.amazonaws.com
    Active connections: 0
  - db2 (Staging Database)
    Local port: 5433
    RDS endpoint: staging-db.us-east-1.rds.amazonaws.com
    Active connections: 1
```

## How It Works

### Daemon Architecture

The service uses a combination of:

1. **PID File** (`/tmp/lazy-ssm.pid`): Tracks the daemon process ID
2. **Unix Socket** (`/tmp/lazy-ssm.sock`): IPC for remote control commands
3. **Command Handlers**: Registered handlers for reload, status, stop, ping

### Process Control

When you run `lazy-ssm start`, the application:
1. Checks if a daemon is already running (via PID file)
2. Forks a new background process
3. Writes the PID to `/tmp/lazy-ssm.pid`
4. Creates a Unix socket at `/tmp/lazy-ssm.sock`
5. Starts listening for connections on configured ports

### Remote Commands

Remote commands (stop, reload, status) work by:
1. Connecting to the Unix socket at `/tmp/lazy-ssm.sock`
2. Sending the command as a string
3. Receiving a JSON response with the result
4. Displaying the result to the user

### Configuration Reload

The reload functionality:
1. Receives reload command via Unix socket
2. Loads new configuration from disk
3. Cancels the context for all running tunnels
4. Waits for all tunnels to shut down gracefully
5. Creates new tunnel instances with updated configuration
6. Starts the new tunnels with a fresh context

This ensures zero downtime for the daemon process itself, while allowing tunnel reconfiguration.

## Example Workflow

### Initial Setup

1. Create your configuration file:
```bash
cat > config.yaml <<EOF
tunnels:
  - name: db1
    description: Production Database
    local_port: 5432
    rds_endpoint: prod-db.us-east-1.rds.amazonaws.com
    rds_port: 5432
    instance_pattern: bastion-*
EOF
```

2. Start the service:
```bash
lazy-ssm start
```

3. Verify it's running:
```bash
lazy-ssm status
```

### Update Configuration

1. Edit the configuration file:
```bash
vim config.yaml
```

2. Reload without downtime:
```bash
lazy-ssm reload
```

3. Verify new configuration:
```bash
lazy-ssm status
```

### Shutdown

1. Stop the service:
```bash
lazy-ssm stop
```

## systemd Integration (Linux)

You can integrate lazy-ssm with systemd:

```ini
[Unit]
Description=Lazy SSM Tunnel Manager
After=network.target

[Service]
Type=forking
ExecStart=/usr/local/bin/lazy-ssm start --config /etc/lazy-ssm/config.yaml
ExecStop=/usr/local/bin/lazy-ssm stop
ExecReload=/usr/local/bin/lazy-ssm reload
PIDFile=/tmp/lazy-ssm.pid
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

Then use standard systemd commands:
```bash
sudo systemctl start lazy-ssm
sudo systemctl stop lazy-ssm
sudo systemctl restart lazy-ssm
sudo systemctl reload lazy-ssm
sudo systemctl status lazy-ssm
```

## launchd Integration (macOS)

Create a launch agent plist at `~/Library/LaunchAgents/com.antero.lazy-ssm.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.antero.lazy-ssm</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/lazy-ssm</string>
        <string>start</string>
        <string>--config</string>
        <string>/Users/yourusername/.lazy-ssm/config.yaml</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/lazy-ssm.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/lazy-ssm.error.log</string>
</dict>
</plist>
```

Load and manage:
```bash
launchctl load ~/Library/LaunchAgents/com.antero.lazy-ssm.plist
launchctl start com.antero.lazy-ssm
launchctl stop com.antero.lazy-ssm
launchctl unload ~/Library/LaunchAgents/com.antero.lazy-ssm.plist
```

## Troubleshooting

### Daemon won't start

Check if it's already running:
```bash
lazy-ssm status
```

Check PID file:
```bash
cat /tmp/lazy-ssm.pid
```

Remove stale PID file:
```bash
rm /tmp/lazy-ssm.pid /tmp/lazy-ssm.sock
```

### Commands not working

Ensure the daemon is running:
```bash
lazy-ssm status
```

Check socket permissions:
```bash
ls -la /tmp/lazy-ssm.sock
```

### Reload fails

Check configuration syntax:
```bash
lazy-ssm start --daemon=false --config config.yaml
```

This will run in foreground and show any configuration errors.
