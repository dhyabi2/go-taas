name: Feature request
description: Suggest an idea or capability for Go TaaS
labels: ["enhancement"]
body:
  - type: markdown
    attributes:
      value: |
        Thanks for the suggestion! Go TaaS is in the design and early-development stage, so design-level ideas are especially welcome — describe the problem you are trying to solve, not just the solution.
  - type: textarea
    id: problem
    attributes:
      label: Problem or use case
      description: What problem does this solve? Who is affected — administrators, agents/SDK consumers, or operators?
    validations:
      required: true
  - type: textarea
    id: solution
    attributes:
      label: Proposed solution
      description: A clear and concise description of what you want to happen.
    validations:
      required: true
  - type: textarea
    id: alternatives
    attributes:
      label: Alternatives considered
      description: Any alternative solutions or workarounds you have considered.
  - type: textarea
    id: additional
    attributes:
      label: Additional context
      description: Any other context — mockups, links to related issues, similar capabilities in other projects.
