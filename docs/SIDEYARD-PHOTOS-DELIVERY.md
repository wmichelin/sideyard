# Private property photos delivery

Status: verified
Last completed: corrected staging image passed large-upload/private-access/delete browser checks.
Now: private property photos available to staging owners.
Next: expanded asset/visit histories and scheduling.
Staging: https://clubdue-staging.michelindigitalmedia.com (verified)
Blocker: none. Production requires separate approval.

## Delivery brief and six-perspective review

- Product manager: owners can attach before/after, damage, access and general
  photos to a property, with captions and upload authorship. Clear saved/error
  feedback and explicit deletion. Start with JPEG/PNG; HEIC needs conversion.
  Job/vehicle/equipment links and crew access remain subsequent slices.
- Architect: authenticated tenant-checked retrieval; never public object URLs.
  Preserve demo upload restrictions. Validate decoded content and dimensions,
  honor JPEG orientation and re-encode to remove metadata. Stable submission
  identity makes retries safe. Deletion must not allow a retry to resurrect data.
- Unit-testing expert: malformed/oversized images, EXIF orientation/stripping,
  cross-tenant and staff access, concurrent quota enforcement, retries/conflicting
  submissions, deletion and backup restoration need behavioral evidence.
- Staff software engineer: separate image normalization, store and HTTP concerns.
  Use existing multipart CSRF/body limits and server-rendered forms. Avoid loading
  image bytes in photo-list queries. Serialize image decoding per server to bound
  peak image-processing memory; reject busy requests with retry guidance.
- DevOps engineer: use existing encrypted Postgres backup for metadata and image
  bytes atomically. Cap each normalized image at 4 MiB and each shop at 100 active
  photos / 50 MiB. Prove exact byte restoration in the production rehearsal.
  No new account, secret, background job or paid service. Monitor database growth
  before broad rollout; migrate bytes to object storage before raising quotas.
- QA tester: real owner upload/view/delete at 390/1440 pixels, unsupported HEIC,
  wrong-tenant/private URLs, preserved caption on error, expired login and demo
  rejection. Confirm deleted bytes cannot be served or restored by form retry.

Decision: proceed with bounded Postgres-backed photo objects for the initial
pilot. This is a deliberate change from the execution plan's separate object
storage starting point: no object store is currently configured, while encrypted
database backup/restore is already exercised. One transaction covers both bytes
and metadata, avoiding partial object writes and orphan cleanup in this slice.
The accepted tradeoff is increased database/backup size. Active image bytes are
bounded per shop; database overhead and retained submission tombstones add
metadata beyond that byte allowance. Do
not describe this as an unlimited media store or increase quotas without a
storage/backup review. The existing Store is the injected persistence boundary.

Deletion immediately removes live image bytes and caption, retaining a minimal
submission tombstone to prevent retry resurrection. Prior encrypted backups may
retain deleted photos until backup retention expires (configured operationally;
the existing rehearsal uses 30 days). Restoring an older backup restores its
state, so recovery must reconcile deletions after the recovery point. Full offline
sync, automatic HEIC conversion and media storage scaling are not in this slice.

## Local verification and deployment review

- Focused image and Postgres tests passed: all eight orientations, both EXIF byte
  orders, malformed offsets, PNG/JPEG metadata removal, unsupported/truncated and
  oversized inputs, private access, concurrent deduplication/count limits, byte
  quota, deletion tombstones, confirmation and caption retention on error.
- Full Go 1.23 `go vet ./...`, required-Postgres `go test -count=1 ./...` and static
  `./cmd/clubdue` build passed with a fresh disposable Postgres 16 database.
- Production rehearsal passed real multipart upload/retry, anonymous denial and
  encrypted backup/restore with matching image-byte/caption/author fingerprint.
- Pilot onboarding rehearsal passed.
- Local Playwright upload/error/view/delete/re-upload passed at 390/1440 pixels;
  no browser errors or document overflow. Phone screenshot inspected.
- Found the live staging proxy lacked an explicit request-size envelope. The
  deployment helper now adds one marked 8 MiB directive beside its unique managed
  upstream. Existing vhost validation, nginx syntax check and rollback remain.
  Only the target Sideyard vhost is changed during deployment. Script syntax and
  repeated proxy rewrite passed; staging will exercise a >1 MiB multipart upload.
- Public-demo uploads and deletion remain rejected. Staging photo upload QA uses
  a dedicated synthetic owner; its temporary tenant will be removed afterward.

## Large-upload staging correction

The first staging browser run failed on its >2 MiB upload after the proxy limit
was corrected. The scratch image had no `/tmp` directory, so Go's multipart
spooling path could not create its temporary file. Confirmed the directory was
absent in the running staging container. Small uploads had passed because they
stayed in memory.

The image now includes an empty `/tmp` directory. The production rehearsal uses
an input larger than 2 MiB, exercises the real scratch container upload/retry path,
then verifies normalized bytes and metadata after encrypted restore. That expanded
rehearsal passed locally. This corrects the release fixture gap; final staging
browser verification must pass again before this slice is marked verified.

## Verified staging result

- Corrected source: `1df43d3dd4140141ad3f05e8fd6e93876c8627a5`.
- CI `34307354003` and Deploy Staging `34307353974` succeeded.
- Image read from running container:
  `ghcr.io/wmichelin/clubdue@sha256:b178cdb5312fc5e20d1cdb10fa4af9853d77e3648373d0dde02bec871bed79aa`.
- Manual `./scripts/smoke-test.sh staging` passed. Playwright at 390/1440 pixels
  passed upload (>2 MiB request through nginx), unsupported-HEIC feedback,
  caption retention, private image headers, anonymous and other-tenant denial,
  delete/re-upload and public-demo upload suppression. Screenshot inspected;
  no document overflow or browser errors. Recent staging logs had no 5xx,
  image-store, deletion or rendering errors.
- Temporary staging owner/tenant and credentials were removed after verification.
  Local QA server and disposable Postgres container were stopped/removed. Scripts
  and screenshots remain in `/tmp/sideyard-qa/photos.cjs` and `photos-*.png`.
- Production remains `7d33ab7ff1c05a796bd351b647f81eea58729726`, image
  `ghcr.io/wmichelin/clubdue@sha256:f54daa3e44102219717683ee877f71117d7555361e3829495ffedcfcecdb1cd2`.
  The production gate remains false; no promotion or production proxy change.

Result: private property photo slice verified on staging. This is not completion
of all E2.4 media associations: vehicle/equipment/job attachments and crew access
remain. HEIC requires conversion; active storage limits remain in force. Owners
reach the feature through Customer → Property details → Private photos.
