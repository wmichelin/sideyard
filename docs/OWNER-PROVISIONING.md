# Owner provisioning and recovery

ClubDue's controlled pilot has no public production enrollment. An operator
provisions the approved owner with the `provision-owner` command; running it a
second time for the same email resets that owner's password, invalidates their
sessions, and writes a `provisioned` or `recovered` audit event in Postgres.

The command is intentionally a one-off operation. It does not run during an
application deploy and it never prints the owner email, password, database URL,
or session secret.

## Operator procedure

After the P1.1 production infrastructure contract exists, place
`OWNER_EMAIL`, `OWNER_PASSWORD`, and `OWNER_SHOP_NAME` in a root-only temporary
environment file, then run a one-off image on the isolated ClubDue network:

```bash
set -a
. /opt/clubdue/production.env
set +a
docker run --rm --network clubdue-production-net \
  --env-file /opt/clubdue/production.env \
  --env-file /root/clubdue-owner-provision.env \
  -e "DATABASE_URL=postgres://clubdue:${POSTGRES_PASSWORD}@clubdue-production-db:5432/clubdue?sslmode=disable" \
  ghcr.io/wmichelin/clubdue:<reviewed-sha> provision-owner
```

The operator records the audited command request in the pilot support log, then
delivers the password through an approved out-of-band channel. Do not put owner
credentials in GitHub Actions, source control, issue comments, terminal logs,
or the staging environment. Delete the temporary owner-provision environment
file immediately after a successful run.

For a recovery, repeat the command with the existing owner email and a new
password. The command returns only `owner provisioned completed` or `owner
recovered completed`; use the database audit event and pilot support log for
the operator record.
