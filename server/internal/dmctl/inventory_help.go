package dmctl

// devicesHelp describes native collection and record reads without contacting a server.
const devicesHelp = `Device inventory

Usage of devices:
  dmctl devices list [--focus combined|apple|managed] [--where JSON]
  dmctl devices get RESOURCE_ID [--raw]
  dmctl devices inspect SERIAL [--raw]
  dmctl devices evidence RESOURCE_ID [--raw]
  dmctl devices resolve APPLE_RESOURCE_RECORD_ID --raw --file mappings.json
  dmctl inventory correlate --file correlation.json
  dmctl devices fields [--raw]
  dmctl devices collect DEVICE_ID

RESOURCE_ID is an independent source record ID from devices list, not a serial,
Apple resource ID or enrollment UDID. Serial inspection finds independent sources without implicitly correlating or resolving.
correlation.json contains {"apple":"APPLE_RECORD_ID","native":"NATIVE_RECORD_ID"}.
Resolution uses caller-declared equivalent paths and requires checked links plus
readRawInventory; complete history, originals, attempts and legacy envelopes also
require this grant. Refreshing source evidence invalidates links until correlation
is explicitly requested again. User Enrollment cannot establish a hardware link.

Native collection uses an enrollment source
record; use devices list --source enrollment to find one.

Collect: request an asynchronous native inventory refresh
  1. Queue supported inventory commands for the enrollment source's local identity.
  2. Request an APNs wake when commands are queued and APNs is configured.
  3. The device contacts the MDM command endpoint and returns command responses.
  4. Acknowledged responses append independent successful source evidence.

An example response is {"queued":5}. It counts queued commands, not returned fields
or completed commands. The count varies with support, enrollments and deduplication.
APNs acceptance does not prove completion. Offline devices retain queued work until
they contact the server; without APNs, collection waits for that contact. A failed
push can return an error after commands were queued: inspect the queue before retrying.

This uses the normal MDM command-polling cycle, not an Authenticate/TokenUpdate
message to CheckInURL. No installed inventory agent is required. Manual collection
bypasses the normal 24-hour freshness interval but retains deduplication.
--wait does not wait for collection; it applies only to axm accounts sync.

Requested native commands (when supported):
  DeviceInformation          Identity, hardware, OS, storage and management indicators.
  SecurityInfo               Detailed security state, including FileVault information.
  ProfileList                Installed profiles and returned payload metadata.
  InstalledApplicationList   Installed applications and their returned metadata.
  CertificateList            Installed certificates, certificate data and identity flags.

DeviceInformation selects queries using platform/version/channel metadata. Returned
fields depend on the OS, hardware and enrollment rights. e.g a macOS 26.6.2 VM
returned these 33 top-level fields; they are not guaranteed for every device:
  SerialNumber, UDID, ProvisioningUDID, DeviceName, HostName, LocalHostName,
  Model, ModelName, ModelNumber, ProductName, IsAppleSilicon, HasBattery,
  BatteryLevel, SupportsLOMDevice, SupportsiOSAppInstalls, DeviceCapacity,
  AvailableDeviceCapacity, OSVersion, BuildVersion, SupplementalBuildVersion,
  TimeZone, SoftwareUpdateDeviceID, IsSupervised, AwaitingConfiguration,
  ActiveManagedUsers, MDMOptions, SystemIntegrityProtectionEnabled,
  IsActivationLockEnabled, IsActivationLockSupported, PINRequiredForDeviceLock,
  PINRequiredForEraseDevice, EACSPreflight, OSUpdateSettings.

The observed OSUpdateSettings contained AutoCheckEnabled,
AutomaticAppInstallationEnabled, AutomaticOSInstallationEnabled,
AutomaticSecurityUpdatesEnabled, BackgroundDownloadEnabled, CatalogURL,
IsDefaultCatalog and PreviousScanDate.

The authenticated enrollment certificate separately supplies identity_certificate_expiry.
Retained DDM values keep their original timestamps; collect does not force a new DDM report.
Purchasing, warranty and AppleCare come from axm accounts sync or inventory sync,
not from devices collect.

Read results:
  dmctl devices get DEVICE_ID
  dmctl devices get DEVICE_ID --raw
  dmctl commands list device ENROLLMENT_ID

Use the enrollment source snapshot's enrollment_id for command-queue inspection.
Check command completion and source observation times: record reads can include older
evidence, and failed refreshes retain previous successful values.

Permissions:
  manageInventory   Queue collection.
  readInventory     Read independent source metadata.
  readRawInventory  Read complete evidence and original source fields with --raw.
Command-queue reads need their own command-read permission. Device-side enrollment
access rights are separate: error 12007 can mean insufficient rights for a command.

Examples:
  dmctl devices collect DEVICE_ID
  dmctl devices get DEVICE_ID --raw
  dmctl inventory export --format csv \
    --where '[{"field":"meta.id","operator":"eq","value":"DEVICE_ID"}]' \
    --columns meta.id,meta.source,meta.account_id,meta.resource_id,meta.observed_at,meta.incomplete

Guide: docs/operations/agentless-inventory.md in GitHub.

Flags (shared across inventory operations; not every flag applies to each verb):
`

// axmHelp documents named connection operations and independent outcome inspection.
const axmHelp = `Named Apple connections

  dmctl axm accounts list|create|get|update|delete|verify|sync ACCOUNT [--file request.json]
  dmctl axm accounts diagnostics ACCOUNT
  dmctl axm accounts services ACCOUNT
  dmctl axm accounts service ACCOUNT SERVICE_ID
  dmctl axm accounts devices ACCOUNT
  dmctl axm accounts device ACCOUNT APPLE_RESOURCE_ID
  dmctl axm accounts coverage ACCOUNT APPLE_RESOURCE_ID
  dmctl axm accounts assigned-service ACCOUNT APPLE_RESOURCE_ID
  dmctl axm accounts assignment-service ACCOUNT
  dmctl axm accounts configure-service ACCOUNT --file binding.json
  dmctl axm accounts assign|unassign ACCOUNT --file intent.json
  dmctl axm accounts operation|operation-log ACCOUNT REQUEST_ID
  dmctl axm accounts jobs ACCOUNT [--cursor CURSOR]
  dmctl axm accounts job ACCOUNT JOB_ID

Device and coverage reads need readBusinessManager and readRawInventory.
Assignment intent contains request_id and serials; binding contains account_revision,
service_id and log_columns. HTTP acceptance queues durable intent. Inspect the
operation for Apple acceptance, terminal log outcomes and separate linkage readback.
Credentials belong in input files and never appear in read responses. Account IDs
are explicit; opaque Apple resource IDs address device reads, serials address intent.
`
