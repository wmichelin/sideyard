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
TF_DATA_DIRs**. Never reuse production state for staging. Create ignored
`staging.local.hcl` / `production.local.hcl` backend files containing:

```hcl
resource_group_name  = "sideyard-terraform-state"
storage_account_name = "<verified-state-account>"
container_name      = "tfstate"
key                 = "staging.tfstate"
use_azuread_auth     = true
subscription_id     = "<verified-subscription-id>"
```

Create matching ignored `staging.local.tfvars` and `production.local.tfvars`
with the variables in `environments/variables.tf`. Keep resource names unique,
operator/app IPv4 addresses explicit and runtime identity object IDs scoped to
the intended environment. Temporary operator/runner IP rules need cleanup when
traveling or after an interrupted automation job.

```sh
export TF_DATA_DIR="$PWD/infra/azure/environments/.terraform/staging"
terraform -chdir=infra/azure/environments init -backend-config=staging.local.hcl
terraform -chdir=infra/azure/environments plan -var-file=staging.local.tfvars -out=staging.tfplan
terraform -chdir=infra/azure/environments apply staging.tfplan
terraform -chdir=infra/azure/environments plan -var-file=staging.local.tfvars
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
database, network and synthetic credentials. It uses no Azure resources. The
application still opens PostgreSQL; the new adapter is an inactive foundation
until the native schema, domain queries and migration/cold-start gates pass.

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

Only the `backups/postgres/` prefix has age-based seven-day cleanup. Native SQL
retention is also seven days. Azure lifecycle scheduling and soft delete can retain
billable backup bytes longer; photo/state versions have no age-deletion rule.

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
