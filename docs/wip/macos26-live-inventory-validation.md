# macOS 26 VM — live inventory validation

Validated on 22 September 2026 against the current inventory implementation.
The enrolled GuestWeave VM returned fresh native MDM inventory to the reference
server, and that evidence was projected into a device record and read through `dmctl`.

## Test environment and provenance

| Item | Verified value |
| --- | --- |
| Source revision | `5467df921145a6e12bc30bc0f05e93ae3d2033d2` |
| Server and CLI | Fresh local builds from that revision; no tracked source modifications |
| macOS | `26.6.2` |
| Build | `25G83` |
| Hardware | `VirtualMac2,1`, reported model name `Virtual Machine` |
| Enrollment | Existing device-channel enrollment, reported supervised |
| Server state | Private copy of the existing VM lab database, migrated by the current server |
| Collection | `dmctl devices collect DEVICE_ID`, followed by native device responses |
| Fresh response window | 2026-09-22 03:34:15–03:34:16 UTC |

The older lab server was not used for these results. The VM's enrollment traffic
was forwarded to the newly built server on local port 18444. SSH was used to establish
that connection and verify the test VM's identity and OS; shell output was not used
to populate the inventory record. No inventory agent was installed.

Binary SHA-256 values:

```text
dmserver bb0a7d0cf8e588bafa673e057403b0a4901b03b9571c47897c48b92524540ea4
dmctl    bc3f032529da54e486e9014d45d958a394d16f5d33492a89ca4ff23e4a1adac8
```

## Fresh command results

| Command | Device result | Inventory obtained |
| --- | --- | --- |
| `DeviceInformation` | Acknowledged | 33 top-level query-response fields, including nested software-update settings |
| `ProfileList` | Acknowledged | One installed profile, with its metadata and returned payload descriptions |
| `CertificateList` | Acknowledged | Five certificates, including common names, certificate data and identity flags |
| `SecurityInfo` | Error `12007` | Device explicitly reported insufficient access rights for this command |
| `InstalledApplicationList` | Error `12007` | Device explicitly reported insufficient access rights for this command |

Both failures were retained as failed source attempts. They do not establish that
macOS lacks these commands. Collecting their information requires an enrollment
granting the corresponding access rights; this run did not replace or broaden the
existing enrollment profile.

## Confirmed device information

| Category | Fields actually returned |
| --- | --- |
| Identity | `SerialNumber`, `UDID`, `ProvisioningUDID`, `DeviceName`, `HostName`, `LocalHostName` |
| Hardware | `Model`, `ModelName`, `ModelNumber`, `ProductName`, `IsAppleSilicon`, `HasBattery`, `BatteryLevel`, `SupportsLOMDevice`, `SupportsiOSAppInstalls` |
| Storage | `DeviceCapacity`, `AvailableDeviceCapacity` |
| OS | `OSVersion`, `BuildVersion`, `SupplementalBuildVersion`, `TimeZone`, `SoftwareUpdateDeviceID` |
| Management | `IsSupervised`, `AwaitingConfiguration`, `ActiveManagedUsers`, `MDMOptions` |
| Security and recovery indicators | `SystemIntegrityProtectionEnabled`, `IsActivationLockEnabled`, `IsActivationLockSupported`, `PINRequiredForDeviceLock`, `PINRequiredForEraseDevice`, `EACSPreflight` |
| Software update | `OSUpdateSettings` |

`OSUpdateSettings` included:

- `AutoCheckEnabled`
- `AutomaticAppInstallationEnabled`
- `AutomaticOSInstallationEnabled`
- `AutomaticSecurityUpdatesEnabled`
- `BackgroundDownloadEnabled`
- `CatalogURL`
- `IsDefaultCatalog`
- `PreviousScanDate`

Selected observed values were Apple Silicon `true`, supervised `true`, System
Integrity Protection enabled `true`, and Activation Lock enabled `false`.
`DeviceCapacity` was `74` and `AvailableDeviceCapacity` was `46`, as returned by
Apple. The VM reported no battery and a `BatteryLevel` sentinel of `-1`.

`EACSPreflight` reported that the MDM-provided bootstrap token failed verification.
This is a returned readiness diagnostic; no erase, token rotation or recovery action
was attempted.

Complete returned evidence is retained. Reviewed normalized fields are available in
ordinary inventory responses; additional source-qualified fields, such as
`mdm.DeviceInformation.QueryResponses.SystemIntegrityProtectionEnabled` and
`mdm.DeviceInformation.QueryResponses.AvailableDeviceCapacity`, require raw-inventory
access in the current implementation.

## MDM enrollment certificate expiry

The record contained a freshly observed `mdm.certificate` source and:

```text
identity_certificate_expiry = 2027-09-17T14:52:56Z
```

The one-device inventory report placed it in `identity_certificate_expiry.over_90_days`.
This expiry comes from the authenticated MDM enrollment identity, independently of
the five certificates returned by `CertificateList`. It is not the APNs certificate
expiry.

## Fields not established by this VM run

| Information | Observed limit |
| --- | --- |
| Wi-Fi, Bluetooth and Ethernet MAC addresses | None appeared in the fresh `DeviceInformation` response; this run does not prove their collection from this VM |
| IMEI, MEID and EID | No real cellular identifiers were returned; this is a virtual Mac |
| Detailed security inventory | `SecurityInfo` was denied by the installed enrollment's access rights |
| Installed applications | `InstalledApplicationList` was denied by the installed enrollment's access rights |
| Purchasing, warranty and AppleCare | No AxM account was exercised; the VM record's purchasing and coverage report values remained unknown |
| MDM migration metadata | No AxM collection was performed; migration facets remained unknown |
| RAM | Reported unavailable by the inventory reporting implementation |
| Fresh DDM status | Existing effective DDM values were backfilled, but their source timestamp remained 2026-09-21; no fresh DDM report is claimed |

The stored record contained 21 public normalized fields and 122 fields in the raw
projection at capture time. These totals include enrollment metadata and retained
DDM evidence; they are not counts of newly returned device fields. The fresh
`DeviceInformation` count is 33.

Two data-quality limitations were visible in the current projection:

- Historical enrollment identity data produced `imei: [""]` and `meid: [""]`.
  These are empty placeholders, not discovered cellular identifiers. Normalization
  should omit empty identifier elements.
- Retained DDM FileVault evidence was present under its source-qualified field,
  while the normalized `filevault_enabled` report facet remained unknown. This run
  does not establish a fresh FileVault measurement or complete normalized mapping
  of DDM status fields.

## API and CLI verification

The freshly built CLI successfully performed:

```text
devices collect DEVICE_ID
devices get DEVICE_ID
devices get DEVICE_ID --raw
inventory reports --where <exact device-ID condition>
inventory export --format csv --where <exact device-ID condition> --columns ...
```

The CSV export returned:

```csv
model,os_version,build_version,apple_silicon,supervised,capacity,identity_certificate_expiry
Virtual Machine,26.6.2,25G83,true,true,74,2027-09-17T14:52:56Z
```

The report counted exactly one selected device, classified it as managed-only,
and recorded a check-in within 24 hours. Native command evidence, public/raw record
reads, reporting and CSV export therefore worked through the current server against
this enrolled macOS VM.

Private raw responses, record snapshots, command outcomes, configuration and binary
provenance remain under the gitignored `test-lab/local/inventory-live/` directory.
Device identifiers, certificate contents and credentials are intentionally omitted
from this report.

See `docs/wip/agentless-device-inventory-scope.md` and the
[operational guide](../operations/agentless-inventory.md) for the broader contract.
