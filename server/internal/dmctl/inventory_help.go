package dmctl

// devicesHelp describes native collection and record reads without contacting a server.
const devicesHelp = `Device inventory

Usage of devices:
  dmctl devices list [--focus combined|apple|managed] [--where JSON]
  dmctl devices get DEVICE_ID [--raw]
  dmctl devices fields [--raw]
  dmctl devices collect DEVICE_ID

DEVICE_ID is the local inventory record ID from devices list, not a serial number,
Apple resource ID or enrollment UDID. Cloud-only records can exist before enrollment.

Collect: request an asynchronous native inventory refresh
  1. Queue supported inventory commands for the record's local enrollments.
  2. Request an APNs wake when commands are queued and APNs is configured.
  3. The device contacts the MDM command endpoint and returns command responses.
  4. Acknowledged responses update the record, query indexes and reports.

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
fields depend on the OS, hardware and enrollment rights. An observed macOS 26.6.2 VM
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

Use the record's enrollment_id for command-queue inspection. Check command completion
and each source's observation/attempt times and errors: record reads can include older
evidence, and failed refreshes retain previous successful values.

Permissions:
  manageInventory   Queue collection.
  readInventory     Read reviewed normalized fields.
  readRawInventory  Read complete evidence and source-qualified fields with --raw.
Command-queue reads need their own command-read permission. Device-side enrollment
access rights are separate: error 12007 can mean insufficient rights for a command.

Examples:
  dmctl devices collect DEVICE_ID
  dmctl devices get DEVICE_ID --raw
  dmctl inventory export --format csv \
    --where '[{"field":"id","operator":"eq","value":"DEVICE_ID"}]' \
    --columns serial_number,model,os_version,filevault_enabled,identity_certificate_expiry

Guide: docs/operations/agentless-inventory.md

Flags (shared across inventory operations; not every flag applies to each verb):
`
