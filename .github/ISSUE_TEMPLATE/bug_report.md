name: Bug report
description: Report something that is not working as expected
labels: ["bug"]
body:
  - type: markdown
    attributes:
      value: |
        Thanks for taking the time to report a bug! Please search existing issues first to avoid duplicates.
  - type: textarea
    id: description
    attributes:
      label: Bug description
      description: A clear and concise description of what the bug is.
    validations:
      required: true
  - type: textarea
    id: steps
    attributes:
      label: Steps to reproduce
      description: Steps to reproduce the behavior.
      placeholder: |
        1. Run '...'
        2. Send a request to '...'
        3. See error
    validations:
      required: true
  - type: textarea
    id: expected
    attributes:
      label: Expected behavior
      description: What you expected to happen instead.
    validations:
      required: true
  - type: textarea
    id: actual
    attributes:
      label: Actual behavior
      description: What actually happened. Include logs and error messages if available.
    validations:
      required: true
  - type: textarea
    id: environment
    attributes:
      label: Environment
      description: Go TaaS version/commit, Go version, Kubernetes version, GPU model, inference engine, etc.
      placeholder: |
        - Go TaaS: main @ <commit>
        - Kubernetes: v1.31
        - GPU: NVIDIA A800
        - Engine: vLLM v0.x
  - type: textarea
    id: additional
    attributes:
      label: Additional context
      description: Any other context — screenshots, configuration, workaround attempts.
