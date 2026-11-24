## Context

D06 stores a high-water cursor and accepts exact repeat or older sequence. Its current `contact.Queue` has a single pending report in private agent state. E03 commits verified process and files in a separate protected journal, and E04/E05/E07 extend local outcomes. E08 must recover both boundaries without recreating either the application or its report.

## Goals / Non-Goals

Connect proven terminal outcomes to the existing HTTPS contact stream. Keep local availability and server-confirmed history distinct. The first quality gate uses production publication and agent entry points with synthetic data. Provisioning, public management API and physical endpoints remain separate.

## Decisions

1. **One authority for status.** Supervisor owns the local result and report outbox together. In application mode the agent keeps the D06 heartbeat worker running and makes a separate bounded reconciliation request to the trusted Unix peer with the transient working key. The request accepts no PIDs, report bodies, certificates or paths from the agent. The supervisor uses the pinned TLS origin and locally bound router ID, checks scope and current key through D03, and uses the existing D06 Cursor/Contact client. No working key, private configuration, PID or raw Xray output enters the report. The regular E02 preparation mode cannot produce a local success.
2. **Atomic transition.** The same protected journal commit writes the application terminal outcome and its report identity. Success needs the same proof across restart and hot paths. Failures use the final durable stage/result after recovery. Keep the local immutable positive outcome and server acceptance separate. Under the shared scope lease one bounded outbox slot prevents a later mutating attempt from superseding the unresolved terminal proof. Neither contact nor download stops. A later desired snapshot coalesces and is reconsidered when the slot resolves. Finalize queue semantics after reading E04/E05/E07 APIs and migrations.
3. **Reservation and acknowledgement.** Read authenticated D06 high-water before first allocation. Reserve one cursor greater than both durable bounds and store the exact report before POST. The queue retains the event's stable ID, cursor and unchanged body until 204 acknowledgement is saved durably. A failed fsync re-establishes the journal barrier before network. A lost response retries the same report. After ack, the protected origin-scoped cursor and completion marker prevent allocating again for the same outcome. Conflicting cursor, corrupt record or unavailable authority fail closed. An authenticated high-water alone does not prove which body was accepted. Release a slot only after acknowledged consumption or a durable retirement with evidence of uncertainty.
4. **Fresh and historical evidence.** Reverify complete exact files and matching runtime including trusted traffic before first send and before retries allowed to advance server state. If proof is lost before assignment, issue the distinct bounded diagnostic `verify/runtime_unknown` while keeping the local original evidence as history. If an assigned success may have reached the server, read authenticated cursor. If the frontier is below the stored sequence, do not send the success again. If it is at or above that sequence, the same body can query consumption through D06 replay without advancing status. A 409 remains a conflict. Record a durable retirement or acknowledgement before queueing a separate evidence-loss diagnostic, with a different stable event ID and a higher cursor; do not mutate the original report. An in-flight old report cannot supersede the later diagnostic. The server marks runtime unknown separately from the last successful revision. Missing proof does not claim rollback failed. E04's actual rollback failure keeps its separate code.
5. **Compatibility and ownership.** Decode the protected journal states already produced by E03–E07. A previously confirmed working generation can produce its first E08 outcome only after re-attesting its existing attempt and proving the delivery state was never initialized. Loss, corruption or restore of the whole authorized journal is an operator boundary when a delivery marker cannot be reconstructed. Do not invent a successful result from high-water. Preserve existing D06 reports from prior agents and server-side compatibility when no new diagnostic is used. DDD separates observable outcomes, report readiness and idempotence in application code. Adapters implement journal, runtime proof and scoped transport.

## Failure Matrix

| Cut | Durable decision |
| --- | --- |
| Local terminal commit, before sequence | Reopen outcome; verify current evidence; reserve at most once. |
| After sequence assignment, before request | Reopen exact body; verify durability and proof; send identical bytes. |
| After server commit, before response | Reuse exact cursor and body; server deduplicates. |
| After response, before local ack | Reopen pending; same body or authenticated conflict; save ack before releasing slot. |
| After local ack or independent heartbeat | No new report or runtime change for the same outcome. |
| External mutation or rollback failure | Keep historical confirmation; distinguish unverified runtime and terminal failure. |

## Risks / Trade-offs

The single slot trades continued mutation during a reporting fault for preserving ordered and verifiable outcomes. Approval of this scope is part of proposal review. The choice cannot prevent an independent privileged operator from changing the runtime immediately after attestation. Acceptance uses protected startup, full observed traffic and reliable evidence at the reporting boundary. Physical power loss and privileged external store rollback require separate verification.
