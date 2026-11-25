## ADDED Requirements

### Requirement: A caller reviews one exact destination before confirming a profile change

The HTTPS API SHALL admit the current external management policy and bind a preview to one router, its active and destination templates, exact proposed inputs, G02 versions, external policy generation and expiry. A read or preview SHALL leave stored assignment, drafts and revisions unchanged. Responses SHALL expose scoped metadata and effects without exposing stored values or validator output.

#### Scenario: First selection and return to a saved destination
- **WHEN** the client previews B from A, separately confirms, and later previews returning to A
- **THEN** the first selection takes B's independent settings and the return uses A's saved values, local rules and reconciled personal order against current A. Only successful confirmation commits the switch.

#### Scenario: Invalid or expired confirmation
- **WHEN** a receipt is missing, stale, expired, replayed after use, from another audience or used with different router, input or destination, or the candidate cannot pass G02
- **THEN** the request fails without changing saved data, assignment or revision. A successful commit followed by a lost response can be resolved by reading the active assignment.

#### Scenario: Authority or visibility changes during validation
- **WHEN** F20 is disabled or rotated, the router becomes hidden, or input versions change after preview or during confirmation
- **THEN** the protected operation rejects stale authority or state at its final boundary and does not commit. The external endpoint cannot issue or manage credentials.
