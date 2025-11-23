# Contributing to k8s-eni-tagger

Thank you for your interest in contributing! We welcome contributions from everyone.

## Development Environment

You will need:

- Go 1.21+
- Docker
- Kubernetes cluster (Kind, Minikube, or EKS)

## Getting Started

1.  Clone the repository:

    ```bash
    git clone https://github.com/prabhu/k8s-eni-tagger.git
    cd k8s-eni-tagger
    ```

2.  Run tests:

    ```bash
    make test
    ```

3.  Build binary:
    ```bash
    make build
    ```

## Pull Requests

1.  Fork the repo and create your branch from `main`.
2.  If you've added code that should be tested, add tests.
3.  Ensure the test suite passes.
4.  Make sure your code lints.

## License

By contributing, you agree that your contributions will be licensed under its Apache 2.0 License.
