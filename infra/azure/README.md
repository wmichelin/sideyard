# Sideyard Azure infrastructure

This provisions the accepted configuration in the
[Azure execution plan](../../docs/SIDEYARD-AZURE-PLAN.md).
[Product map](../../docs/PRODUCT-MAP.md) describes deployed product behavior;
[delivery status](../../docs/SIDEYARD-DELIVERY-STATUS.md) records actual provisioning
and release evidence. A successful Terraform validation is not a deployed app.

The database uses free-limit pause, 0.5–1 vCore, a 60-minute idle delay, a 32 GB
maximum and explicit seven-day local native backup retention. Blob accounts use
Hot LRS, private containers, Entra authentication and versioning. No rule expires
photo/state versions merely because their original creation date is old.

## CLI setup

Use a native Azure CLI, or the checked-in Docker wrapper pinned to Azure CLI
2.90.0. Terraform is pinned by its required version and provider lock files.

```sh
export PATH="$PWD/scripts/azure/bin:$PATH"
az login --use-device-code
az account list --output table
az account set --subscription '<verified-subscription-id>'
```

Complete the device sign-in in Safari or any browser. An authenticated Microsoft
identity without an Azure subscription cannot provision resources. Before apply,
verify the subscription's free-service eligibility, original account start date,
SQL free-offer slots/region, total storage allowance and existing metric alerts.
Register only Microsoft.Storage, Microsoft.Sql, Microsoft.Insights,
Microsoft.Authorization and Microsoft.OperationalInsights when needed, using CLI.
Provider auto-registration is disabled to avoid unrelated subscription changes.

The current tenant's Conditional Access policy rejects Azure CLI device login
from an unregistered macOS device (`AADSTS530035`). Staging therefore also has a
dedicated certificate-authenticated `sideyard-staging-terraform` service
principal. Its private key and PFX stay in mode-0600 files under
`~/.config/sideyard-azure`; Azure stores only the public certificate. The identity
has Contributor, Role Based Access Control Administrator and Locks Contributor
only on `sideyard-staging`, plus Storage Blob Data Contributor only on the
Terraform-state account. It has no production assignment and no client secret.
Set `ARM_CLIENT_ID`, `ARM_TENANT_ID`, `ARM_SUBSCRIPTION_ID`,
`ARM_CLIENT_CERTIFICATE_PATH`, `ARM_CLIENT_CERTIFICATE_PASSWORD` and
`ARM_USE_AZUREAD` from the protected local environment before Terraform commands.

The current staging runner inventory is intentionally public metadata:

- application/client ID: `f4bb176e-5532-4f16-8412-bcb1278e8222`
- service-principal object ID: `b2fece45-783c-4eff-99ac-ecb8f92cc6de`
- certificate SHA-256 fingerprint:
  `36:7F:2D:23:7D:63:9D:8A:BA:53:60:EB:49:36:B0:C7:F5:C5:3D:F7:12:04:E4:F1:AC:80:C2:8C:18:5B:3B:89`
- certificate expiry: `2028-09-10T00:03:29Z`

Audit it at least monthly and before every apply. Verify the local directory is
mode 0700, each key/PFX/environment file is mode 0600, the certificate is valid,
and `az account show` reports the exact tenant, subscription and client ID above.
The principal must have only Contributor, Role Based Access Control Administrator
and Locks Contributor on `sideyard-staging`, plus Storage Blob Data Contributor on
the state account. Treat any subscription-wide or production assignment as a
failure. In Cloud Shell, also check `az ad app credential list --id <client-id>`;
the app must have certificates only and no password credential.

Rotate with overlap: create a new private key locally, upload only its public
certificate in Cloud Shell, authenticate locally with the new PFX, run an empty
Terraform plan, then remove the old certificate by its key ID. Never remove the
old certificate until the new one passes all three checks. If every local copy is
lost or expired, use the subscription-owner account in Cloud Shell to upload a
replacement public certificate; Azure cannot recover the private key. Record the
new fingerprint and expiry here in the same reviewed change.

## Bootstrap remote state

Create `bootstrap/operator.local.tfvars` with subscription ID, operator object
ID, a unique state account name and explicit public operator IPv4 addresses.
Use local permissions `0600`; it is ignored by Git. No passwords or account keys
are needed. The initial bootstrap state is local: protect and back it up until
it is migrated to its own remote state key. Never destroy the state account as
part of an application rollback.

```sh
terraform -chdir=infra/azure/bootstrap init
terraform -chdir=infra/azure/bootstrap plan -var-file=operator.local.tfvars -out=bootstrap.tfplan
# Review the plan and offer eligibility, then apply that exact saved plan.
terraform -chdir=infra/azure/bootstrap apply bootstrap.tfplan
```

Entra RBAC assignments can take time to propagate. If initial container creation
receives a permission error, confirm the assignment and retry the same reviewed
configuration; do not enable account keys or public access to work around it.

## Separate environment state

`environments/` is a shared configuration used with **different backend keys and
TF_DATA_DIRs**. Never reuse production state for staging. The reviewed staging
backend and variables are checked in as `staging.hcl` and `staging.tfvars`; they
contain resource names and principal IDs, never credentials. Production remains
in ignored `production.local.hcl` and `production.local.tfvars` files. Local
staging copies may remain for older operator helpers but must match the tracked
files.

```hcl
resource_group_name  = "sideyard-terraform-state"
storage_account_name = "<verified-state-account>"
container_name      = "tfstate"
key                 = "staging.tfstate"
use_azuread_auth     = true
subscription_id     = "<verified-subscription-id>"
```

Keep the ignored `staging.local.hcl` alias used by older operator helpers equal
to `staging.hcl`. Create only the production local files from the variables in
`environments/variables.tf`. Keep resource names unique, operator/app IPv4
addresses explicit and runtime identity object IDs scoped to the intended
environment. Temporary operator/runner IP rules need cleanup when traveling or
after an interrupted automation job.

```sh
export TF_DATA_DIR="$PWD/infra/azure/environments/.terraform/staging"
terraform -chdir=infra/azure/environments init -backend-config=staging.hcl
terraform -chdir=infra/azure/environments plan -var-file=staging.tfvars -out=staging.tfplan
terraform -chdir=infra/azure/environments apply staging.tfplan
terraform -chdir=infra/azure/environments plan -var-file=staging.tfvars
```

The last plan should be empty. Keep saved plans private and delete them after
use. CLI `az rest` must read back the database's free flags, SKU, minimum capacity,
pause delay and backup policy, plus storage versioning/private-access settings.
Check the SQL `earliestRestoreDate` after native backups initialize; seven-day
retention configuration alone does not prove a recoverable backup exists.

Creating a SQL resource does not port the existing PostgreSQL application.
Application migration, photo-version recovery, alerts and the production cutover
must pass the execution plan's independent gates.

The SQL Server connection/migration boundary can be tested from a checkout with
`./scripts/sqlserver-rehearsal.sh`. It builds the tests, creates an isolated local
SQL Server Developer container, runs required real-engine tests, and removes its
database, network and synthetic credentials. It uses no Azure resources. Web
traffic still opens PostgreSQL; Azure SQL HTTP startup remains fail-closed.

## Staging SQL operator boundary

Only the checked-in wrapper may run native database commands against Azure:

```sh
export PATH="$PWD/scripts/azure/bin:$PATH"
az account show --output table
python3 scripts/azure/sql-operator.py staging check-db
python3 scripts/azure/sql-operator.py staging migrate
python3 scripts/azure/sql-operator.py staging check-db
python3 scripts/azure/sql-operator.py staging maintenance
```

The wrapper reads the exact server and database from the isolated staging
Terraform state, checks the active Azure CLI subscription against the resource
ID, builds a temporary static binary from the checkout, and supplies only the
operator settings. The binary independently requires `APP_ENV=staging`, an
Azure host beginning `sideyard-staging-`, database `sideyard`, Azure CLI auth,
no client credential, and no `DATABASE_URL`. It logs the non-secret target before
connecting. Commands time out after 15 minutes and release all connections when
the process exits. The scratch application image has no Azure CLI and is not the
operator execution environment.

`check-db` is read-only and verifies the complete embedded migration ledger plus
snapshot isolation settings. `migrate` enables the required isolation settings,
applies pending native migrations under a session lock, and performs the same
check. `maintenance` checks the schema first, retires only the exact legacy
staging demo, then runs visit, demo-expiry, recurrence, renewal, and digest work.
It is repeatable and stops on its first failure.

The staging HTTP runtime uses a separate Entra application and a contained
database user. Create its credential once, without an Azure resource role, then
bootstrap only its database permissions with the administrator operator:

```sh
export SIDEYARD_SQL_RUNTIME_CLIENT_ID=292cb17a-202e-4489-bea7-f5ded18fcca8
python3 scripts/azure/provision-sql-runtime.py staging \
  --expected-client-id "$SIDEYARD_SQL_RUNTIME_CLIENT_ID"
python3 scripts/azure/sql-operator.py staging bootstrap-runtime \
  --runtime-client-id "$SIDEYARD_SQL_RUNTIME_CLIENT_ID"
python3 scripts/azure/sql-operator.py staging check-db
```

The first command derives the exact database and subscription from staging
Terraform state, checks the signed-in Azure CLI account, and verifies the
tracked `sideyard-staging-sql-runtime` identity, credential key and expiry. Its
secret stays in the mode-0600
`~/.config/sideyard-azure/staging-sql-runtime.env`. On the identity's first-ever
creation, omit `--expected-client-id` and supply `--expires`; an exact
pre-existing display name is otherwise rejected unless its client ID is already
in the protected local file or explicitly supplied. The command creates no
Azure RBAC assignment and exits nonzero when the key is missing, mismatched,
expired, or within 30 days of expiry. Run this check at least monthly and route
failure to the operational email alert before activation.

The second command creates the fixed contained SQL user from the application ID,
grants application DML through `sideyard_app_runtime`, and denies migration-ledger
writes. It is repeatable and rejects a different identity behind the fixed alias.
Runtime configuration must include both `AZURE_SQL_CLIENT_ID` and the equal
`AZURE_SQL_RUNTIME_CLIENT_ID`; `/readyz` verifies the fixed alias, external-user
type, exact SID, permissions and schema. Do not copy the credential into Terraform
state, shell history, logs, the Blob identity or the administrator operator.

Install the protected file only after the source revision has passed review. Use
a root-only temporary transfer and preserve mode 0600; never paste the secret
into a command, workflow input or log. Before selecting Azure in the staging
deployment, start the reviewed image on an unproxied loopback port with
`--env-file staging-sql-runtime.env`, `APP_ENV=staging`, a strong session secret
and the staging public URL. Check the public entry pages and `/livez` first,
then `/readyz`, an isolated demo POST and its dashboard. Stop it with
`docker stop --timeout 110` followed by `docker rm`.

Rotate without an outage by retaining both keys until the new one passes that
canary:

```sh
python3 scripts/azure/provision-sql-runtime.py staging --rotate \
  --expected-client-id "$SIDEYARD_SQL_RUNTIME_CLIENT_ID" \
  --expires 2028-09-10T23:59:59Z
# Install the new mode-0600 file and run the unproxied canary.
python3 scripts/azure/provision-sql-runtime.py staging \
  --expected-client-id "$SIDEYARD_SQL_RUNTIME_CLIENT_ID" \
  --revoke-key-id OLD_KEY_ID_REPORTED_BY_ROTATION
```

Rotation saves the old file as `staging-sql-runtime.env.previous`. Before old-key
revocation, `--rollback-rotation` restores it atomically; reinstall it and rerun
the canary, then revoke the failed replacement key. Successful old-key revocation
removes the saved file. The ordinary check reports any remaining stale key IDs.
Never revoke the key named by the current protected file.

Production commands are rejected by this slice. Enabling production requires a
separate reviewed implementation with an exact artifact gate, verified backup
and reconciliation evidence, explicit approval, and least-privilege production
identities. The conditional HTTP runtime requires this distinct client identity
and its contained database grants; do not grant DDL to it or reuse the Blob photo
or backup identities.

### Conditional host-owned work and rollback fence

Azure HTTP configuration requires `SCHEDULED_WORK_OWNER=host`, the fixed
`INTEREST_WAKE_FILE=/var/run/sideyard/interest.wake`, and a UUID data generation
supplied by the host selector. The deploy helper maintains a root-owned mode-0600
`/var/lib/sideyard-operations/staging-backend.env`, validates the running and
retained container labels/markers against it, and refuses an ordinary backend
or generation change. It omits `DATABASE_URL` and does not start PostgreSQL when
the authoritative backend is Azure SQL. Changing that selector is reserved for
the final transfer procedure; editing the SQL credential file cannot select a
backend.

Install `runtime-state.sh`, `run-interest-delivery.sh`, and
`run-database-window.sh` under `/opt/clubdue/azure-tools/azure`; install
`sideyard-interest@.service`, `sideyard-interest@.path`,
`sideyard-interest-reconcile@.service`, `sideyard-interest-reconcile@.timer`,
`sideyard-database-window@.service`, and `sideyard-database-window@.timer` under
`/etc/systemd/system`; and install `sideyard-tmpfiles.conf` under
`/etc/tmpfiles.d`. Run `systemd-tmpfiles --create`, `systemctl daemon-reload`,
and `systemd-analyze verify` over all six units. Confirm the timer target with
`systemctl show -p Triggers sideyard-interest-reconcile@staging.timer` and the
path target with `systemctl show -p Triggers sideyard-interest@staging.path`
before enabling either. Keep all new units disabled while PostgreSQL is
authoritative. At the later reviewed Azure cutover,
the interest path and one-shot boot reconciliation replace the embedded worker.
The unified 07:00 UTC database-window timer replaces the old backup timer only
after slice 5 supplies a tested Azure logical export/restore. Until then,
`run-backup.sh` deliberately refuses to report a stale PostgreSQL export as an
Azure backup.

Each atomic backup-success record includes the authoritative backend and data
generation. The monitor rejects an otherwise recent marker when either differs,
and checks the unified database-window service after Azure becomes authoritative.

The immediate host worker and deploy helper share
`/run/sideyard-locks/sideyard-staging-database-window.lock` inside a root-owned
mode-0700 directory. They acquire it before reading the backend selector. A wake invokes
`docker exec clubdue-staging-current /clubdue deliver-interest`; the daily
window invokes `scheduled-work`, then attempts its verified backup even when
maintenance or delivery fails. Both commands are bounded and exit, leaving no
leadership connection. A stopped retained app has `restart=no`; rollback starts
it only after its backend and generation match the selector.

If access fails, confirm the CLI account and staging firewall allowlist, then
change the allowlist through a reviewed Terraform plan. Do not add an ad hoc SQL
login or widen the firewall. Remove obsolete operator addresses afterward while
preserving the application address. A failed or interrupted command is followed
by `check-db`; migration checksums and the session lock make a retry safe. Record
the reviewed source SHA, start/end time, target printed by the wrapper, command
result, and Azure backup restore window in the delivery evidence.

## Current deployment and remote recovery

Subscription `ddd8d30e-b827-461d-bf25-09f77337cbdd` uses tenant
`c930f3e5-c279-40c0-9278-f61a280098ee`. State lives in
`sideyardstateddd8d30e/tfstate`: separate `bootstrap.tfstate`, `staging.tfstate`
and `production.tfstate` keys. Application resources are in Central US; the tiny
state account is in East US. Keep the matching backend key with each TF_DATA_DIR.

The existing bootstrap state has been migrated remotely. On another checkout,
create ignored `bootstrap/backend.local.tf` containing:

```hcl
terraform {
  backend "azurerm" {}
}
```

Initialize it using the same backend settings as above with key `bootstrap.tfstate`.
The original account bootstrap on a new subscription still starts without this
file, then uses `init -migrate-state -force-copy` after reviewing both state locations.
Do not copy an empty local state over the existing remote state.

When traveling, add the current public operator IPv4 to the state account through
`az storage account network-rule add`, and to the relevant Terraform allowlist.
CLI control-plane access does not need a storage data-plane firewall exception.
Apply the reviewed plan and remove obsolete operator IPs; preserve the app's
`45.55.214.46` entry. Browserless operations work with a valid CLI login or service
principal; Microsoft can still require interactive reauthentication after expiry.

## Private photo integration

Create a dedicated identity with `scripts/azure/provision-identity.py staging
photos <account> --expires <ISO-time>` (or `production`). It stores credentials
in `~/.config/sideyard-azure/<environment>-photos.env`, mode 0600, outside Git.
Set its printed object ID in `runtime_object_ids` and apply Terraform. The custom
role permits read/write within `photos`; it has no deletion or backup access.
The current credentials expire September 1, 2027; rotate before then.

Install the file as `/opt/clubdue/staging-azure.env` or `production-azure.env`,
root-owned mode 0600. The deployment helper passes it to the app as data. An
absent file leaves PostgreSQL photo storage in use. A production configuration
change takes effect only on the reviewed production deployment.

After the Azure-enabled app is deployed, backfill or resume interrupted uploads:

```sh
ssh pantry-droplet 'docker exec clubdue-staging-current /clubdue sync-photos'
# For an approved production release, use clubdue-current.
```

The command handles at most 1,000 photos / 15 minutes and returns a nonzero exit
status if further work or retry is needed. It can be repeated without duplicate
versions. Resume is currently **operator-run**, not a periodic worker. New-photo
form retries also resume their own upload. Existing photos remain readable during
backfill failures; unconfirmed new photos reserve quota and stay hidden.

Keep database recovery bytes until a later, separately verified migration removes
them. To recover without Azure, restore the complete authenticated PostgreSQL
backup into an isolated database and start the app **without all four Azure photo
variables**. No cloud access is needed to read the retained copies. Turning Azure
back on resumes exact-version reads. An SQL Server cutover will require a different
data-aware rollback procedure; this slice has not performed that cutover.

## Seven-day encrypted exports

Use a separate `backups` identity from `provision-identity.py`; set its object ID
in `backup_object_ids`. Keep its mode-0600 environment file in the operator's
secret storage and `/opt/clubdue/azure/<environment>-backups.env`. Add independent
random `CLUBDUE_BACKUP_PASSPHRASE` and `CLUBDUE_BACKUP_HMAC_KEY` values. Preserve
these secrets outside the droplet: without them an encrypted export cannot be
restored. The export identity can read/write only `backups`, not photos or deletes.

Install the backup/auth scripts under `/opt/clubdue/azure-tools/` preserving the
`azure/` subdirectory, and the checked-in systemd units under `/etc/systemd/system/`.
Start the matching service once and verify recovery before enabling its timers:

```sh
systemctl start sideyard-azure-backup@staging.service
systemctl enable --now sideyard-azure-backup@staging.timer sideyard-backup-monitor@staging.timer
systemctl list-timers --all
journalctl -u sideyard-azure-backup@staging.service
```

Daily backups run around 07:00 UTC. Local exports use a new directory
`/var/backups/sideyard-azure/<environment>`; historical backup retention is preserved.
The uploader authenticates the ciphertext, streams create-only objects, reads back
their exact Azure versions, then publishes an authenticated completion manifest.
Exports include full inline photo recovery data. Each encrypted export is capped
at 256 MiB to bound free storage use; exceeding it fails visibly and alerts the
owner rather than pretending the backup completed.

PostgreSQL exports and logical completion markers have age-based seven-day
cleanup. Logical ciphertext components use a separate prefix with eight-day
cleanup, so a retained completion marker cannot outlive the exact versions it
references and failed uploads still expire. The publisher refuses to reuse an
existing component name and allows at most one hour from its first component write
to completion, based on exact-version Azure `Last-Modified` metadata checked again
during retrieval. The publisher checks trusted Azure server time before the marker
write and reserves five minutes for its bounded creation/readback. Retry an
interrupted publication with a freshly built export and export ID. Native SQL
retention is also seven days. Azure lifecycle scheduling and soft delete can retain
billable backup bytes longer; photo/state versions have no age-deletion rule.

### Logical recovery identities (staging preparation only)

Logical recovery uses two certificate-only Entra applications instead of the
runtime photo, Terraform or legacy backup identities. `logical-backup-publisher`
receives Blob read/write only at the staging `backups` container;
`logical-photo-reader` receives Blob read only at the staging `photos` container.
Neither role includes deletion or management actions. Container RBAC cannot itself
require a version query or conditional PUT, so `logical-backup-store.py` enforces
exact versions and create-only writes.

Generate a distinct RSA client key and public certificate for each purpose under
the mode-0700 `~/.config/sideyard-azure` directory. Key, certificate and combined
PEM files must be mode 0600. Upload only the public certificates through the
signed-in owner's Cloud Shell. Each single-tenant application must have one exact
human recovery owner, its two purpose tags, no redirect URI, API permission,
federated credential or password, and one matching certificate. Cloud Shell and
chat must never receive the private keys, combined PEMs, assertions or tokens.

The owner bootstrap returns only client, principal, certificate-key and expiry
metadata. Bind each result locally before assigning RBAC:

```sh
python3 scripts/azure/verify-recovery-certificate.py staging logical-backup-publisher \
  --client-id '<publisher-client-id>' \
  --principal-object-id '<publisher-principal-id>' \
  --credential-key-id '<publisher-key-id>' \
  --credential-expires '<publisher-expiry>' \
  --credential-thumbprint '<publisher-thumbprint>' \
  --require-unassigned
python3 scripts/azure/verify-recovery-certificate.py staging logical-photo-reader \
  --client-id '<reader-client-id>' \
  --principal-object-id '<reader-principal-id>' \
  --credential-key-id '<reader-key-id>' \
  --credential-expires '<reader-expiry>' \
  --credential-thumbprint '<reader-thumbprint>' \
  --require-unassigned
```

The Cloud Shell JSON is the owner attestation for the application owner, tags,
redirect/API/federation/password inventory and certificate key ID; archive its
non-secret fields with release evidence. The ARM-only verifier cannot independently
read Microsoft Graph. It does compare the attested Azure certificate thumbprint
and expiry with the protected local certificate and then proves the client,
principal and tenant together from the signed Blob-token claims.

The verifier accepts no storage override and has no Microsoft Graph write path.
It derives the exact account, containers and roles from remote Terraform state,
checks file owner/mode and key/certificate equality, matches the local fingerprint
and expiry to the protected binding, authenticates with the certificate in an
ephemeral Azure CLI profile, and audits effective ARM assignments in every enabled
subscription visible to the ARM-only runner. The two principals must remain
distinct from each other, the operator, runtime, Terraform, photo and legacy
backup identities.

Put the verified principal IDs into `logical_backup_publisher_object_ids` and
`logical_photo_reader_object_ids`, inspect a staging plan, and apply that exact
plan. Terraform preconditions reject overlap, multiple principals and every
production recovery assignment. After RBAC propagation, rerun both verifier
commands without the five binding arguments and with `--require-assigned` until
each reports `assignment_status=assigned`.

Use the two protected files separately to prove the
publisher can conditionally create and exactly read back a disposable object only
under a unique `logical-components/staging/canary-*` key in `backups`; the eight-day
lifecycle removes the non-deletable test object. The reader can read an exact
version of an operator-created synthetic canary object in `photos`. Test reader
write/delete only against that synthetic object. Also prove management and
cross-container requests return authorization failure rather than accepting a
missing-object response. Do not target customer photos, install either file on
the host, or activate logical publication until those canaries are recorded.

Run both verifier commands monthly with `--require-assigned` and alert on failure
or expiry within 30 days. In the same audit, the owner must repeat and archive the
Cloud Shell Graph inventory for exact owner/tags, no redirects, API permissions,
federation or passwords, and the expected certificate set; the ARM-only verifier
cannot detect later Graph-side drift. Rotate with certificate overlap: generate distinct
`.next` key/certificate files locally, upload only the new public certificate in
Cloud Shell, authenticate it and run the complete positive and negative canary,
then atomically replace the protected binding and remove the old public certificate
by its exact key ID. Keep at most two certificates during that window. If the new
certificate fails, retain the current binding and remove the replacement. A
dedicated rotation command and its interruption tests are required before the
first rotation; do not improvise file replacement.

To decommission an identity, first remove its object ID through a reviewed
Terraform apply and verify the role assignment is gone. Then remove its public
certificates and delete the service principal and application as separate owner
actions. Never delete an app while RBAC still names its principal.

### Staging PostgreSQL logical-export source

The native transfer reads PostgreSQL through a separate database login. It does
not reuse the application owner, Azure runtime principal or either Blob identity.
After the exact source revision has passed CI and staging verification, bootstrap
the dormant source roles from the CLI with the manual workflow:

```sh
gh workflow run staging-postgres-exporter.yml --ref main -f action=bootstrap
gh run list --workflow staging-postgres-exporter.yml --limit 1
# After provisioning, rerun an independent read-only audit:
gh workflow run staging-postgres-exporter.yml --ref main -f action=check
```

The workflow copies the reviewed wrapper and runtime-state helper only to a
SHA-scoped temporary directory, acquires the existing host database-operation lock,
verifies the immutable running revision, and then executes and removes both
candidates. It never overwrites durable shared tooling. It rejects production,
Azure authority, a stale workflow revision, a mutable running image or a mismatched
data generation, and runs the exact binary already serving staging in a short-lived
read-only container.

The bootstrap creates one fixed dormant login,
`sideyard_staging_logical_exporter`, and one no-login grant role,
`sideyard_logical_exporter`. It creates no password or credential file. Effective
access is limited to the authoritative `clubdue` database, `USAGE` on `public`,
and column-level `SELECT` on `schema_migrations.version/checksum` plus the exact
columns in the binary's current logical-transfer profile. The dormant login has a
single-connection ceiling, read-only defaults and bounded statement, lock and
idle-transaction times. The audit rejects database `CREATE`/temporary access,
other databases or schemas, extra relation/column grants, views, sequences,
large objects, application functions, source-schema drift, role authority and
row-level security on source tables.

The dedicated Sideyard PostgreSQL cluster has only its database-owning application
administrator. Bootstrap verifies that condition first, then permanently removes
the cluster's broad `PUBLIC` database/schema/relation/function/large-object defaults;
the owner
keeps implicit access. This baseline is not rolled back when the dormant exporter
is later removed. If another login or database owner exists, bootstrap refuses to
change anything and that principal must be reviewed first. The current slice
supplies dormant source grants only; the later export command must implement a
short-lived, crash-recoverable password activation and disable it after use.

Download an exact completed backup using protected credentials in the process
environment (never inline command arguments):

```sh
mkdir -m 700 /tmp/sideyard-recovery
python3 scripts/azure/backup-store.py download postgres/<timestamp>/manifest.json /tmp/sideyard-recovery
# Then use scripts/postgres-restore.sh with an isolated, explicitly named DB.
```

`/var/lib/sideyard-backups/<environment>.last-success` records the verified
manifest key. The monitor checks hourly and after service failure; it emails
`wmichelin@gmail.com` through the existing protected Resend configuration when a
job fails or no verified export exists for 26 hours. Repeated notices for the
same incident are suppressed. This host-based monitor cannot report total droplet
loss; independent external uptime/freshness monitoring remains future work.

Azure SQL quota alerts use the owner's Azure Monitor action group. The free-trial
subscription rejected the **test-notifications** API (`Free subscription not
supported`), so creation of the rules is verified but synthetic Azure email
delivery is not. No paid upgrade was made to bypass this restriction.
