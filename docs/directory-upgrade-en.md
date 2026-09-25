# Directory Structure Upgrade Guide

> 中文版: [directory-upgrade.md](directory-upgrade.md)

This document describes the framework's directory structure upgrade process, including manual upgrade steps and an automation script.

## Directory Structure Changes

### Main Changes

1. Utilities moved to the `pkg/utils` directory:
   - `pkg/addr` → `pkg/utils/addr`
   - `pkg/uuid` → `pkg/utils/uuid`
   - `pkg/libs/bloom` → `pkg/utils/bloom`

2. Service-related components moved to the `pkg/service` directory:
   - `pkg/tracing` → `pkg/service/telemetry`
   - `pkg/logger` → `pkg/service/logger`
   - `pkg/discover` → `pkg/service/discover`
   - `pkg/core` → `pkg/service/core`

3. Client package structure refined:
   - `pkg/client/resty` → `pkg/client/http`
   - `pkg/client/grpcclient` unchanged (gRPC client package path)

4. Example code reorganized:
   - `example/auth-ratelimit` → `examples/security/auth-ratelimit`
   - `example/circuitbreaker` → `examples/resilience/circuitbreaker`
   - `example/timeout` → `examples/resilience/timeout`
   - `example/svc` → `examples/services/svc`
   - `example/example` → `examples/complete/example`

## Automated Upgrade Script

Save the following as `upgrade-directory.sh`:

```bash
#!/bin/bash

# Exit on error
set -e

echo "Starting directory structure upgrade..."

# Create new directories
echo "Creating new directory structure..."
mkdir -p pkg/utils/{addr,uuid,bloom} \
        pkg/service/{telemetry,logger,discover,core} \
        pkg/client/{grpcclient,http,nacos} \
        examples/{security,resilience,services,complete}

# Move utility files
echo "Moving utility files..."
mv pkg/addr/* pkg/utils/addr/ 2>/dev/null || true
mv pkg/uuid/* pkg/utils/uuid/ 2>/dev/null || true
mv pkg/libs/bloom/* pkg/utils/bloom/ 2>/dev/null || true

# Move service-related components
echo "Moving service-related components..."
mv pkg/tracing/* pkg/service/telemetry/ 2>/dev/null || true
mv pkg/logger/* pkg/service/logger/ 2>/dev/null || true
mv pkg/discover/* pkg/service/discover/ 2>/dev/null || true
mv pkg/core/* pkg/service/core/ 2>/dev/null || true

# Restructure client packages
echo "Restructuring client packages..."
mv pkg/client/resty/* pkg/client/http/ 2>/dev/null || true

# Reorganize example code
echo "Reorganizing example code..."
mv example/auth-ratelimit examples/security/ 2>/dev/null || true
mv example/circuitbreaker examples/resilience/ 2>/dev/null || true
mv example/timeout examples/resilience/ 2>/dev/null || true
mv example/svc examples/services/ 2>/dev/null || true
mv example/example examples/complete/ 2>/dev/null || true

# Clean up old directories
echo "Cleaning up old directories..."
rm -rf pkg/{addr,uuid,libs,tracing,logger,discover,core} pkg/client/resty example 2>/dev/null || true

# Update import paths
echo "Updating import paths..."
find . -type f -name "*.go" -exec sed -i '' \
    -e 's|"github.com/rushteam/beauty/pkg/addr"|"github.com/rushteam/beauty/pkg/utils/addr"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/uuid"|"github.com/rushteam/beauty/pkg/utils/uuid"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/libs/bloom"|"github.com/rushteam/beauty/pkg/utils/bloom"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/tracing"|"github.com/rushteam/beauty/pkg/service/telemetry"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/logger"|"github.com/rushteam/beauty/pkg/service/logger"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/discover"|"github.com/rushteam/beauty/pkg/service/discover"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/core"|"github.com/rushteam/beauty/pkg/service/core"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/client/grpcclient"|"github.com/rushteam/beauty/pkg/client/grpcclient"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/client/resty"|"github.com/rushteam/beauty/pkg/client/http"|g' \
    -e 's|"github.com/rushteam/beauty/example/example/api/v1"|"github.com/rushteam/beauty/examples/complete/example/api/v1"|g' \
    {} +

# Update import paths in README.md
echo "Updating import paths in docs..."
find . -type f -name "*.md" -exec sed -i '' \
    -e 's|"github.com/rushteam/beauty/pkg/discover/etcdv3"|"github.com/rushteam/beauty/pkg/service/discover/etcdv3"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/tracing"|"github.com/rushteam/beauty/pkg/service/telemetry"|g' \
    {} +

echo "Directory structure upgrade complete!"
echo "Please run 'go mod tidy' to update dependencies."
```

## Usage

1. Save the upgrade script in the project root:

```bash
curl -o upgrade-directory.sh https://raw.githubusercontent.com/rushteam/beauty/main/docs/upgrade-directory.sh
```

2. Make the script executable:

```bash
chmod +x upgrade-directory.sh
```

3. Run the upgrade script:

```bash
./upgrade-directory.sh
```

4. Update dependencies:

```bash
go mod tidy
```

## Notes

1. Before running the upgrade script, it is recommended to:
   - Commit or stash your current code changes
   - Create a new git branch
   - Back up important files

2. After the script runs, you need to:
   - Check that import paths were updated correctly
   - Run the tests to make sure everything works
   - Check that the build passes

3. If you use a custom directory structure or custom file names, some files may need manual adjustment

## Manual Upgrade Steps

If you prefer to upgrade manually, follow these steps:

1. Create the new directory structure
2. Move the relevant files to their new locations
3. Update import paths
4. Remove the old, empty directories
5. Update dependencies

## FAQ

1. Q: After upgrading, the build fails because a package cannot be found?
   A: Check the dependency versions in `go.mod` and run `go mod tidy` to update dependencies.

2. Q: Some files were not moved correctly?
   A: Check that file permissions and paths are correct; move the files manually if necessary.

3. Q: Import paths were not fully updated?
   A: Use your IDE's global search to find the old import paths and update them manually.

## Rollback

If you need to roll back the changes:

1. If you use git:
   ```bash
   git reset --hard HEAD
   git clean -fd
   ```

2. If you made a manual backup:
   - Restore the backed-up files
   - Delete the newly created directories
