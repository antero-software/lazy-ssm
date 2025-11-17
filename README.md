# Lazy SSM Tunnel Manager

On-demand AWS Systems Manager tunnels to RDS databases through EC2 bastion instances.

## Features

- **On-Demand Tunnels**: Automatically start SSM tunnels when connections are detected
- **Automatic Cleanup**: Close idle tunnels after configurable timeout periods
- **Multiple Databases**: Manage multiple database tunnels simultaneously
- **Configuration-Based**: YAML configuration with validation
- **AWS Integration**: Support for AWS profiles and regions with automatic SSO login
- **Instance Auto-Discovery**: Use wildcard patterns to find EC2 instances dynamically
- **SSO Authentication**: Automatic detection and browser-based login for AWS SSO profiles
- **Graceful Shutdown**: Clean termination of all tunnels on exit

## Prerequisites

- Go 1.21 or later
- AWS CLI installed and configured
- AWS SSM plugin for AWS CLI (`session-manager-plugin`)
- EC2 instances with SSM agent installed
- Appropriate IAM permissions for SSM sessions

## Installation

### From Source

```bash
git clone https://github.com/jeffory/lazy-ssm.git
cd lazy-ssm
go build
```

## Configuration

### Quick Start

1. Copy the example configuration:
```bash
cp config.example.yaml config.yaml
```

2. Edit `config.yaml` with your AWS resources:
```yaml
tunnels:
  - name: my-database
    local_port: 3306
    rds_endpoint: my-db.cluster-xxxxx.us-east-1.rds.amazonaws.com
    rds_port: 3306
    instance_id: i-xxxxx
    description: "My Database"
```

3. Run the tunnel manager:
```bash
./lazy-ssm
```

### Configuration File

The configuration file (`config.yaml`) supports the following sections:

#### Application Settings
```yaml
app:
  log_level: info  # Options: debug, info, warn, error
```

#### AWS Settings
```yaml
aws:
  profile: ""      # Global AWS profile (optional, uses default if empty)
  region: ""       # Global AWS region (optional, uses default if empty)
  cli_path: aws    # Path to AWS CLI executable
```

**Note**: Global AWS settings can be overridden per tunnel. See "Per-Tunnel AWS Profiles" below.

#### Timeout Settings
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

#### Network Settings
```yaml
network:
  listen_address: localhost  # Address to bind listeners
  tunnel_port_offset: 10000  # Offset for SSM tunnel ports
```

#### Tunnel Definitions
```yaml
tunnels:
  - name: production-db              # Unique name for this tunnel
    local_port: 3306                 # Local port to listen on
    rds_endpoint: prod-db.cluster-xxxxx.us-east-1.rds.amazonaws.com
    rds_port: 3306                   # RDS port
    instance_id: i-xxxxx             # EC2 bastion instance (explicit ID)
    description: "Production DB"     # Human-readable description
    aws_profile: production          # Optional: Override AWS profile for this tunnel
    aws_region: us-east-1            # Optional: Override AWS region for this tunnel
```

**Note**: Each tunnel requires a unique `local_port`. Ports below 1024 require root privileges.

**Instance Discovery**: You can use either:
- `instance_id`: Explicit EC2 instance ID (e.g., `i-xxxxx`)
- `instance_pattern`: Wildcard pattern to auto-discover instances (e.g., `app-server-*`)

#### Per-Tunnel AWS Profiles

You can specify different AWS profiles and regions for individual tunnels. This is useful when:
- Managing databases across multiple AWS accounts
- Working with resources in different regions
- Using different IAM credentials per environment

Per-tunnel AWS settings take precedence over global settings:

```yaml
# Global AWS settings (fallback)
aws:
  profile: default
  region: us-east-1

tunnels:
  # Uses global settings (default profile, us-east-1)
  - name: dev-db
    local_port: 3306
    rds_endpoint: dev-db.cluster-xxxxx.us-east-1.rds.amazonaws.com
    rds_port: 3306
    instance_id: i-xxxxx
    description: "Dev DB"

  # Override with staging profile
  - name: staging-db
    local_port: 3307
    rds_endpoint: staging-db.cluster-xxxxx.us-east-1.rds.amazonaws.com
    rds_port: 3306
    instance_id: i-yyyyy
    description: "Staging DB"
    aws_profile: staging

  # Override with different account and region
  - name: customer-db
    local_port: 5432
    rds_endpoint: customer-db.cluster-xxxxx.eu-west-1.rds.amazonaws.com
    rds_port: 5432
    instance_id: i-zzzzz
    description: "Customer DB"
    aws_profile: customer-account
    aws_region: eu-west-1
```

#### Instance Auto-Discovery

Instead of specifying an explicit `instance_id`, you can use `instance_pattern` to automatically discover EC2 instances by their Name tag. This is particularly useful for:
- Auto-scaling groups where instance IDs change frequently
- Dynamic environments where instances are regularly replaced
- Managing multiple environments with similar naming conventions

**How it works**:
1. The pattern matches against the EC2 instance **Name tag**
2. Supports wildcard matching using `*` (e.g., `bastion-*`, `*-production`, `app-*-server`)
3. Only considers **running** instances with **SSM agent** installed
4. Automatically caches the discovered instance ID for the session
5. Re-discovers if the tunnel needs to restart (e.g., after idle timeout)

**Examples**:

```yaml
tunnels:
  # Using explicit instance ID
  - name: prod-db
    local_port: 3306
    rds_endpoint: prod-db.cluster-xxxxx.us-east-1.rds.amazonaws.com
    rds_port: 3306
    instance_id: i-0123456789abcdef0      # Explicit ID
    description: "Production DB"

  # Using wildcard pattern for auto-scaling group
  - name: staging-db
    local_port: 3307
    rds_endpoint: staging-db.cluster-xxxxx.us-east-1.rds.amazonaws.com
    rds_port: 3306
    instance_pattern: bastion-staging-*    # Matches any "bastion-staging-*" instance
    description: "Staging DB"

  # Using pattern with different AWS profile
  - name: dev-db
    local_port: 3308
    rds_endpoint: dev-db.cluster-xxxxx.us-west-2.rds.amazonaws.com
    rds_port: 3306
    instance_pattern: dev-bastion-*
    description: "Dev DB"
    aws_profile: development
    aws_region: us-west-2
```

**Important**:
- Cannot use both `instance_id` and `instance_pattern` - choose one
- Pattern matching requires proper IAM permissions for `ec2:DescribeInstances`
- If multiple instances match the pattern, the first one found is used
- The instance must be running and have SSM agent installed

#### AWS SSO Authentication

The tunnel manager automatically detects when AWS profiles require SSO authentication and handles the login process for you.

**How it works**:
1. Before starting each tunnel, the manager checks if the AWS profile is authenticated
2. If credentials are expired or missing, it detects whether the profile uses SSO
3. For SSO profiles, it automatically runs `aws sso login --profile <name>`
4. Your browser opens to the AWS SSO login page
5. After successful authentication, the tunnel starts automatically

**Benefits**:
- No need to manually run `aws sso login` before using the tool
- Works with multiple AWS accounts and SSO configurations
- Automatic re-authentication when tokens expire
- Each tunnel can use a different SSO profile

**Example with SSO**:

```yaml
aws:
  profile: default  # Global profile (optional)

tunnels:
  # Production account with SSO
  - name: prod-db
    local_port: 3306
    rds_endpoint: prod-db.cluster-xxxxx.us-east-1.rds.amazonaws.com
    rds_port: 3306
    instance_pattern: bastion-prod-*
    aws_profile: production-sso    # SSO profile
    description: "Production DB"

  # Customer account with different SSO
  - name: customer-db
    local_port: 3307
    rds_endpoint: customer-db.cluster-xxxxx.eu-west-1.rds.amazonaws.com
    rds_port: 3306
    instance_pattern: bastion-*
    aws_profile: customer-sso      # Different SSO profile
    aws_region: eu-west-1
    description: "Customer DB"
```

**What you'll see**:
```
[SSO] Profile 'production-sso' requires authentication. Opening SSO login page...
[SSO] Running: aws sso login --profile production-sso
[SSO] Profile 'production-sso' successfully authenticated
[Production DB] Discovering instance matching pattern: bastion-prod-*
[Production DB] Discovered instance: i-0123456789abcdef0
[Production DB] Starting SSM tunnel on port 13306...
```

### Configuration Validation

The application validates configuration on startup and will report errors for:
- Missing required fields
- Invalid port numbers
- Duplicate local ports
- Invalid AWS instance IDs
- Invalid log levels
- Missing instance_id or instance_pattern (must have one)
- Both instance_id and instance_pattern specified (mutually exclusive)

## Usage

### Basic Usage

Start all configured tunnels:
```bash
./lazy-ssm
```

### Using a Custom Config File

```bash
./lazy-ssm --config /path/to/config.yaml
```

### Override AWS Profile

```bash
./lazy-ssm --profile production
```

### Override AWS Region

```bash
./lazy-ssm --region us-west-2
```

### Combined Options

```bash
./lazy-ssm --config prod-config.yaml --profile production --region us-west-2
```

### Help

```bash
./lazy-ssm --help
```

## How It Works

1. **Listening**: The tunnel manager listens on configured local ports
2. **On-Demand**: When a connection is detected, an SSM tunnel is started automatically
3. **Proxying**: Traffic is proxied between the client and RDS through the SSM tunnel
4. **Idle Detection**: After the configured idle timeout, unused tunnels are closed
5. **Reconnection**: Tunnels automatically restart when new connections arrive

## Architecture

```
Client → Local Port → Lazy SSM Manager → SSM Tunnel → EC2 Bastion → RDS Database
  (e.g., 3306)              ↓                 (e.g., 13306)
                     Auto-start/stop
                     based on activity
```

## Example: Connecting to MySQL

Once the tunnel manager is running:

```bash
# Connect using standard MySQL client
mysql -h localhost -P 3306 -u username -p

# Or use any MySQL GUI tool pointing to localhost:3306
```

## Project Structure

```
lazy-ssm/
├── main.go              # Application entry point with Cobra CLI
├── config/
│   ├── config.go        # Configuration loading with Viper
│   └── validation.go    # Configuration validation
├── tunnel/
│   ├── tunnel.go        # LazySSMTunnel implementation
│   └── manager.go       # TunnelManager coordination
├── ec2/
│   └── discovery.go     # EC2 instance discovery by pattern
├── sso/
│   └── auth.go          # AWS SSO authentication handling
├── config.yaml          # Your configuration (gitignored)
├── config.example.yaml  # Example configuration template
├── go.mod               # Go module definition
└── README.md            # This file
```

## Security Considerations

- **Configuration Files**: The `config.yaml` file is gitignored by default as it may contain sensitive resource IDs
- **AWS Credentials**: Never commit AWS credentials. Use AWS CLI profiles or IAM roles
- **Network Exposure**: Tunnels listen on localhost by default to prevent external access
- **Port Permissions**: Use ports ≥1024 to avoid requiring root privileges

## Troubleshooting

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
5. Check the AWS profile and region are correct for the tunnel
6. Use `--log-level debug` to see detailed discovery information

### SSO authentication issues

1. Verify your AWS CLI is configured for SSO: `aws configure get sso_start_url --profile <name>`
2. Check your SSO configuration in `~/.aws/config`
3. Ensure you have network access to the SSO login page
4. Try manual login first: `aws sso login --profile <name>`
5. Verify SSO session hasn't been revoked in AWS IAM Identity Center

### Connection refused

1. Verify the tunnel is configured correctly in `config.yaml`
2. Check the logs for tunnel startup errors
3. Ensure the RDS endpoint is correct
4. Verify security groups allow traffic from the bastion instance

### Permission denied on port

Ports below 1024 require root privileges. Either:
- Use ports ≥1024 in your configuration
- Run with sudo (not recommended)

## Development

### Building

```bash
go build -o lazy-ssm
```

### Running Tests

```bash
go test ./...
```

### Adding Dependencies

```bash
go get github.com/example/package
go mod tidy
```

## License

MIT License - See LICENSE file for details

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.
