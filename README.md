# Lazy SSM Tunnel Manager

On-demand AWS Systems Manager tunnels to RDS databases through EC2 bastion instances.

## Features

- **On-Demand Tunnels**: Automatically start SSM tunnels when connections are detected
- **Automatic Cleanup**: Close idle tunnels after configurable timeout periods
- **Multiple Databases**: Manage multiple database tunnels simultaneously
- **Multi-Account Support**: Per-tunnel AWS profile and region overrides for cross-account setups
- **Instance Auto-Discovery**: Use wildcard patterns to find EC2 instances dynamically
- **SSO Authentication**: Automatic detection and browser-based login for AWS SSO profiles
- **Graceful Shutdown**: Clean termination of all tunnels on exit

## Prerequisites

- AWS CLI installed and configured
- AWS SSM plugin for AWS CLI (`session-manager-plugin`)
- EC2 instances with SSM agent installed
- Appropriate IAM permissions for SSM sessions

## Installation

### macOS

```bash
brew install antero-software/lazy-ssm/lazy-ssm
```

### Linux

Install [Homebrew for Linux](https://brew.sh) if not already installed, then:

```bash
brew install antero-software/lazy-ssm/lazy-ssm
```

`brew services` on Linux uses systemd, so the same commands work on both platforms.

### From Source

```bash
git clone https://github.com/antero-software/lazy-ssm.git
cd lazy-ssm
go build
```

## Setup

1. Create your configuration file at `~/.lazy-ssm/config.yaml`:

```yaml
tunnels:
  - name: my-database
    local_port: 23306
    rds_endpoint: my-db.cluster-xxxxx.us-east-1.rds.amazonaws.com
    rds_port: 3306ß
    instance_id: i-xxxxx
    description: "My Database"
```
Note: For local port numbers, use ports ≥1024 to avoid requiring root privileges. Random ports are recommended to prevent conflicts with existing services. The tunnel manager will automatically start tunnels on demand when connections are detected on these ports.

2. Start the service:

```bash
brew services start lazy-ssm
```

3. Connect to your database as normal — the tunnel starts automatically on first connection:

```bash
mysql -h localhost -P 23306 -u username -p
```

## Running as a Service

lazy-ssm is designed to run as a Homebrew service. It starts automatically on login and restarts if it crashes.

```bash
brew services start lazy-ssm      # start
brew services stop lazy-ssm       # stop
brew services restart lazy-ssm    # restart
brew services list                 # check status
```

### Commands

```bash
lazy-ssm status        # show brew service status
lazy-ssm tail          # show last 25 log lines
lazy-ssm tail -n 50    # show last 50 log lines
lazy-ssm version       # show version
```

Logs are written to `$(brew --prefix)/var/log/lazy-ssm.log`.

### Foreground Mode

For debugging, run directly in the foreground — logs go to stdout and `Ctrl+C` shuts it down cleanly:

```bash
lazy-ssm
lazy-ssm --config /path/to/config.yaml
```

## Configuration

The configuration file is loaded from `~/.lazy-ssm/config.yaml` by default. You can override this with `--config`.

### Application Settings

```yaml
app:
  log_level: info  # debug, info, warn, error
```

### AWS Settings

```yaml
aws:
  profile: ""      # Global AWS profile (optional, uses default if empty)
  region: ""       # Global AWS region (optional, uses default if empty)
  cli_path: aws    # Path to AWS CLI executable
```

Global AWS settings can be overridden per tunnel.

### Timeout Settings

```yaml
timeouts:
  tunnel_ready: 15s          # Maximum wait for tunnel to become ready
  tunnel_ready_poll: 500ms   # How often to check tunnel readiness
  idle_check: 30s            # How often to check for idle tunnels
  idle_timeout: 5m           # Close tunnel after inactivity
  shutdown: 10s              # Graceful shutdown timeout
  shutdown_poll: 100ms       # Shutdown poll interval
  connection: 1s             # TCP connection timeout
```

### Network Settings

```yaml
network:
  listen_address: localhost  # Address to bind listeners
  tunnel_port_offset: 10000  # Offset for SSM tunnel ports
```

### Tunnel Definitions

```yaml
tunnels:
  - name: production-db              # Unique name for this tunnel
    local_port: 3306                 # Local port to listen on
    rds_endpoint: prod-db.cluster-xxxxx.us-east-1.rds.amazonaws.com
    rds_port: 3306                   # RDS port
    instance_id: i-xxxxx             # EC2 bastion instance (explicit ID)
    description: "Production DB"     # Human-readable description
    aws_profile: production          # Optional: override AWS profile for this tunnel
    aws_region: us-east-1            # Optional: override AWS region for this tunnel
```

Each tunnel requires a unique `local_port`. Ports below 1024 require root privileges.

Use either `instance_id` (explicit) or `instance_pattern` (wildcard auto-discovery) — not both.

### Instance Auto-Discovery

Use `instance_pattern` to match EC2 instances by their Name tag. Useful for auto-scaling groups where instance IDs change frequently.

```yaml
tunnels:
  - name: staging-db
    local_port: 3307
    rds_endpoint: staging-db.cluster-xxxxx.us-east-1.rds.amazonaws.com
    rds_port: 3306
    instance_pattern: bastion-staging-*
    description: "Staging DB"
```

- Supports `*` wildcards (e.g., `bastion-*`, `*-production`)
- Only matches running instances with SSM agent installed
- Requires `ec2:DescribeInstances` IAM permission
- If multiple instances match, the first one found is used

### Per-Tunnel AWS Profiles

```yaml
aws:
  profile: default
  region: us-east-1

tunnels:
  - name: dev-db
    local_port: 3306
    rds_endpoint: dev-db.cluster-xxxxx.us-east-1.rds.amazonaws.com
    rds_port: 3306
    instance_id: i-xxxxx
    description: "Dev DB"

  - name: customer-db
    local_port: 5432
    rds_endpoint: customer-db.cluster-xxxxx.eu-west-1.rds.amazonaws.com
    rds_port: 5432
    instance_id: i-zzzzz
    description: "Customer DB"
    aws_profile: customer-account
    aws_region: eu-west-1
```

### AWS SSO Authentication

The tunnel manager automatically detects when a profile requires SSO and handles login for you.

1. Before starting a tunnel, it checks if the profile is authenticated
2. If credentials are expired, it runs `aws sso login --profile <name>`
3. Your browser opens to the AWS SSO login page
4. After login, the tunnel starts automatically

```
[SSO] Profile 'production-sso' requires authentication. Opening SSO login page...
[SSO] Profile 'production-sso' successfully authenticated
[Production DB] Discovering instance matching pattern: bastion-prod-*
[Production DB] Discovered instance: i-0123456789abcdef0
[Production DB] Starting SSM tunnel on port 13306...
```

## How It Works

```
Client → Local Port → Lazy SSM Manager → SSM Tunnel → EC2 Bastion → RDS Database
  (e.g., 3306)              ↓                 (e.g., 13306)
                     Auto-start/stop
                     based on activity
```

1. **Listening**: The tunnel manager listens on configured local ports
2. **On-Demand**: When a connection is detected, an SSM tunnel is started automatically
3. **Proxying**: Traffic is proxied between the client and RDS through the SSM tunnel
4. **Idle Detection**: After the configured idle timeout, unused tunnels are closed
5. **Reconnection**: Tunnels automatically restart when new connections arrive

## Project Structure

```
lazy-ssm/
├── main.go                    # Entry point
├── cmd/
│   ├── root.go                # Root command and tunnel manager startup
│   ├── status.go              # Service status via brew services
│   ├── tail.go                # Log tail command
│   └── version.go             # Version command
├── config/
│   ├── config.go              # Configuration loading with Viper
│   └── validation.go          # Configuration validation
├── tunnel/
│   ├── tunnel.go              # Tunnel implementation and connection proxying
│   └── manager.go             # Tunnel lifecycle coordination
├── ec2/
│   └── discovery.go           # EC2 instance discovery by Name tag pattern
├── sso/
│   └── auth.go                # AWS SSO authentication handling
└── .github/
    └── workflows/
        ├── release.yml         # Release workflow
        └── update-homebrew.yml # Homebrew formula update
```

## Security Considerations

- **Configuration Files**: `~/.lazy-ssm/config.yaml` may contain sensitive resource IDs — keep it private
- **AWS Credentials**: Never commit AWS credentials. Use AWS CLI profiles or IAM roles
- **Network Exposure**: Tunnels listen on `localhost` by default to prevent external access
- **Port Permissions**: Use ports ≥1024 to avoid requiring root privileges

## Troubleshooting

### Service won't start

```bash
brew services list              # check service state
lazy-ssm tail                   # check recent logs
lazy-ssm --config ~/.lazy-ssm/config.yaml  # run in foreground to see errors directly
```

### Tunnel fails to start

1. Verify AWS CLI is installed: `aws --version`
2. Verify SSM plugin is installed: `session-manager-plugin --version`
3. Check AWS credentials: `aws sts get-caller-identity`
4. Verify EC2 instance has SSM agent running
5. Check IAM permissions for SSM sessions

### Instance discovery fails

1. Verify the Name tag on your EC2 instances matches the pattern
2. Check that you have `ec2:DescribeInstances` IAM permission
3. Ensure at least one matching instance is in **running** state
4. Verify the instance has an IAM instance profile (required for SSM)

### SSO authentication issues

1. Check your SSO configuration: `aws configure get sso_start_url --profile <name>`
2. Try manual login first: `aws sso login --profile <name>`
3. Verify SSO session hasn't been revoked in AWS IAM Identity Center

### Connection refused

1. Check `lazy-ssm tail` for tunnel startup errors
2. Ensure the RDS endpoint is correct
3. Verify security groups allow traffic from the bastion instance

### Permission denied on port

Use ports ≥1024 in your configuration, or run with sudo (not recommended).

## Development

```bash
go build -o lazy-ssm    # build
go test ./...           # run tests
```

## License

MIT License - See LICENSE file for details

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.
