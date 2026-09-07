## 1. Container environment

- [x] 1.1 Add and build the Entware image; verify opkg package inventory and record pinned upstream versions.
- [x] 1.2 Attempt XKeen startup in an isolated container; record the result and provide the direct-Xray fallback if firmware dependencies prevent a usable start.

## 2. Configuration and verification

- [x] 2.1 Add split API/inbound/outbound/routing files and persistent configuration storage; verify `xray run -test -confdir` and startup succeed.
- [x] 2.2 Add repeatable traffic checks for full rule replacement with unchanged process, persistence after restart and rejection of an invalid candidate; execute checks successfully.

## 3. Documentation

- [x] 3.1 Document commands, observed outcomes and compatibility limits, link the lab from README and validate the OpenSpec change.
