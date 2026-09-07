# router-lab Specification

## Purpose

Изолированный стенд позволяет проверить Entware/opkg, загрузку файлов и применение правил через настоящий Xray API до подключения физических роутеров.

## Requirements

### Requirement: Real Entware environment
The lab SHALL provide real Entware/opkg in `/opt`, with a documented build and startup command, without mounting host router configurations or publishing the Xray API externally.

#### Scenario: Start the lab
- **WHEN** a developer builds and starts the lab using Docker
- **THEN** opkg can list installed Entware packages and the container provides `/opt/etc/init.d` and `/opt/etc/xray/configs`

### Requirement: XKeen attempt and explicit Xray fallback
The experiment SHALL attempt to run a pinned XKeen revision and record the outcome. If firmware dependencies prevent a usable start, the lab SHALL run Xray directly in the same Entware environment and report the limitation without claiming XKeen compatibility.

#### Scenario: Firmware integration is unavailable
- **WHEN** XKeen fails due to unavailable firmware integration
- **THEN** the experiment documents the concrete failure and validates the Xray fallback

### Requirement: Split configuration and live routing verification
The lab SHALL load separate API, inbound, outbound and routing JSON files. Its repeatable verification SHALL demonstrate replacement of the full routing ruleset via Xray API without restarting the process, then save the routing file and verify behavior after restart.

#### Scenario: Replace rules and persist
- **WHEN** a valid ruleset changes a test destination from allowed to blocked
- **THEN** a fresh proxied request is blocked, the original process remains alive, and the blocked behavior remains after saving and restarting

#### Scenario: Invalid candidate
- **WHEN** the complete candidate configuration is invalid
- **THEN** verification rejects it before applying and the last working configuration remains usable

### Requirement: Bounded compatibility claims
The lab SHALL document the tested versions, architecture and commands and distinguish Xray config/API checks from firmware firewall integration, other CPU architectures and product agent behavior.

#### Scenario: Xray checks pass
- **WHEN** verification succeeds in Xray mode
- **THEN** results do not describe XKeen-only exclusion files or firmware hooks as tested
