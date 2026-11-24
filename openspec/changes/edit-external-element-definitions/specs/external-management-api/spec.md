## ADDED Requirements

### Requirement: External client updates definitions of an existing element
The service SHALL accept a conditional PATCH of one existing element's ordered definitions under the external access policy, retaining the source and all other elements. An omitted default SHALL remain absent and a supplied default source SHALL retain its exact valid JSON source text under the editable F08 rules.

#### Scenario: Successful conditional edit
- **WHEN** an authorized client supplies the current F21 ETag, selected UUID and definitions with the existing ordered names
- **THEN** the service returns the complete committed F21 response and its strong ETag; the next GET returns the same bytes

#### Scenario: Refused mutation
- **WHEN** the permit is revoked or its generation changes before commit, the ETag is stale, the element is absent, or the ordered names differ
- **THEN** the operation preserves the existing draft and reports the corresponding access, precondition, missing-resource or invalid-definition error
