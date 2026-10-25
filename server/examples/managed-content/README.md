# Explicit managed content

Enable sealed SQL storage and `DM_APPSBOOKS_MDM_ID` with your stable MDM identity.
Register the downloaded location token using a private JSON file containing
`revision` (zero for a new location) and `sToken`. Grant the dedicated actions in
[the operations guide](../../../docs/operations/apps-and-books.md#reference-server-operations).
Replace example asset, enrollment and serial values with explicitly designated
resources; the examples grant no permission to use Apple resources.

```sh
dmctl appsbooks register -file /private/location.json london
dmctl appsbooks get london
dmctl appsbooks job-create -file licensing-job.json london designated-license
dmctl appsbooks submit -file /private/worker.json london designated-license
dmctl appsbooks job-get london designated-license
dmctl managed-content create -file assign-and-install.json designated-install
dmctl managed-content get designated-install
```

`worker.json` contains `{"worker":"operator","leaseSeconds":60}`. Submission and
content creation are separate explicit operations. The supervised content worker
advances queued operations; explicit job submission and polling remain available.
Use current location revisions. Ownership is configured separately and never
claimed by registration or refresh. Avoid submitting both examples for the same
resource unintentionally: `assign-and-install` requests its own licensing job.
Ordinary inspection omits credentials and raw source evidence. MDM acknowledgment,
DDM publication and device-reported current installation/version are distinct.
