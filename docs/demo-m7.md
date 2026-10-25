# M7 demo — endurance & release

An executable walkthrough of the M7 surfaces. It doubles as the shape of the
Gate G7 fresh-install demo (which must complete in <15 minutes on a clean
machine). Commands assume `make build` has run (`./bin/athanor`).

> **Runtime.** `sh scripts/install.sh` is the supported path (Core Pod:
> images, config, container, health). The direct `state/` and `sqlite3` commands
> in this walkthrough are the **host development** path; the Core Pod keeps
> state in the `athanor-state` volume. `backup`/`restore -container` operate on
> that volume.

## 1. Doctor and start

```bash
./bin/athanor doctor                       # §30.2 host checks
sh scripts/install.sh                      # Core Pod: images, config, start, health
# or, for the host development path:
# make jobpod-image && ./bin/athanor start -host -skip-doctor
```

## 2. A project and a goal

```bash
PID=$(./bin/athanor project create -name demo -archetype code \
        -goal "Write a Python module that adds two numbers." -test-command "pytest -q")
./bin/athanor goal submit -project "$PID" -goal "Add a subtract function with tests."
./bin/athanor job watch -job <id>
```

## 3. Power / daydreaming

Leave the machine idle on AC for longer than `agent.idle_threshold`; the
power supervisor enters the `autonomous` profile and the daydream loop runs
its §17.1 actions. Inspect the audit trail:

```bash
sqlite3 state/athanor.db "SELECT action, started_at FROM daydream_logs ORDER BY started_at DESC LIMIT 5;"
```

## 4. Alarms

```bash
./bin/athanor alarms                 # GET /alarms; a critical alarm freezes the daemon
./bin/athanor alarms -resolve <id>
```

## 5. Morning Digest

```bash
./bin/athanor digest -hours 12
```

## 6. Backups and restore

```bash
# Core Pod (supported): snapshot inside the state volume, then copy it out.
./bin/athanor backup -container athanor-core -keep 5
podman cp athanor-core:/var/lib/athanor/state/backups/<snapshot>.db .
podman stop athanor-core
./bin/athanor restore -container athanor-core -from ./<snapshot>.db -force
```

## 7. Kill switch

```bash
./bin/athanor freeze
./bin/athanor unfreeze -reason "demo complete"
```

The remaining Gate G7 arms (24h soak, fresh-install timing, security-audit
sign-off) are human checkpoints; see [`soak-m7.md`](soak-m7.md) and
[`m7-gate-g7.md`](m7-gate-g7.md).
