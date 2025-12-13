# Pre-commit Setup Guide

This repository uses [pre-commit](https://pre-commit.com/) to ensure code quality and consistency before commits.

## Installation

### 1. Install pre-commit

**macOS/Linux (via pip):**
```bash
pip install pre-commit
```

**macOS (via Homebrew):**
```bash
brew install pre-commit
```

**Windows:**
```bash
pip install pre-commit
```

### 2. Install the Git Hook Scripts

```bash
cd /path/to/k8s-eni-tagger
pre-commit install
```

This will install the git hook scripts into your `.git/hooks/` directory.

### 3. Install Required Tools

The pre-commit hooks require several tools to be installed:

#### golangci-lint (Go linting)
```bash
# macOS
brew install golangci-lint

# Linux
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin

# Or via Go
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

#### Helm (Chart linting)
```bash
# macOS
brew install helm

# Linux - see https://helm.sh/docs/intro/install/
```

#### yamllint (YAML validation)
```bash
pip install yamllint
```

#### markdownlint (Markdown linting)
```bash
npm install -g markdownlint-cli
```

#### hadolint (Dockerfile linting)
```bash
# macOS
brew install hadolint

# Linux
wget -O /usr/local/bin/hadolint https://github.com/hadolint/hadolint/releases/download/v2.12.0/hadolint-Linux-x86_64
chmod +x /usr/local/bin/hadolint
```

## Usage

### Automatic (on git commit)

Once installed, the hooks will run automatically when you commit:

```bash
git add .
git commit -m "Add new feature"
# Pre-commit hooks run automatically here
```

### Manual Execution

Run all hooks on all files:
```bash
pre-commit run --all-files
```

Run specific hook:
```bash
pre-commit run golangci-lint --all-files
pre-commit run helmlint --all-files
```

Run on staged files only:
```bash
pre-commit run
```

### Update Hooks

Update hook repositories to the latest version:
```bash
pre-commit autoupdate
```

## Configured Hooks

| Hook | Description | Auto-fix |
|------|-------------|----------|
| `go-fmt` | Format Go code | ✅ Yes |
| `go-vet` | Check Go code for errors | ❌ No |
| `go-mod-tidy` | Clean up go.mod/go.sum | ✅ Yes |
| `golangci-lint` | Comprehensive Go linting | ✅ Some |
| `yamllint` | Validate YAML syntax | ❌ No |
| `helmlint` | Validate Helm charts | ❌ No |
| `trailing-whitespace` | Remove trailing whitespace | ✅ Yes |
| `end-of-file-fixer` | Ensure newline at EOF | ✅ Yes |
| `check-yaml` | Validate YAML files | ❌ No |
| `markdownlint` | Lint Markdown files | ✅ Some |
| `hadolint` | Lint Dockerfiles | ❌ No |

## Skipping Hooks

### Skip all hooks for a commit (not recommended)
```bash
git commit -m "Your message" --no-verify
```

### Skip specific hook
```bash
SKIP=golangci-lint git commit -m "Your message"
```

### Skip multiple hooks
```bash
SKIP=golangci-lint,yamllint git commit -m "Your message"
```

## Troubleshooting

### Hook fails with "command not found"

Ensure the required tool is installed and in your PATH:
```bash
which golangci-lint
which helm
which yamllint
```

### golangci-lint is slow

The first run may be slow as it builds cache. Subsequent runs will be faster.

### YAML validation fails on Helm templates

Helm templates are excluded from `check-yaml` hook (see `.pre-commit-config.yaml`).

### Update pre-commit config

If you modify `.pre-commit-config.yaml`, run:
```bash
pre-commit install --install-hooks
```

## Integration with CI/CD

The same checks can be run in CI/CD pipelines:

```yaml
# Example GitHub Actions workflow
- name: Run pre-commit
  run: |
    pip install pre-commit
    pre-commit run --all-files
```

## Make Targets

The repository also provides Make targets for linting:

```bash
make lint          # Run golangci-lint
make helm-lint     # Run helm lint
make fmt           # Run go fmt
make vet           # Run go vet
```

## Configuration Files

- `.pre-commit-config.yaml` - Pre-commit hook configuration
- `.golangci.yaml` - golangci-lint settings
- `.yamllint.yaml` - yamllint rules

## References

- [pre-commit Documentation](https://pre-commit.com/)
- [golangci-lint Documentation](https://golangci-lint.run/)
- [Helm Lint Documentation](https://helm.sh/docs/helm/helm_lint/)
