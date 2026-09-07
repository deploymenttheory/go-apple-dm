# Reference catalogue

This catalogue indexes primary protocol sources and repositories relevant to design review.
Repository topics identify why a source is useful; they do not claim current maintenance,
compatibility, completeness or suitability as a dependency. The module files define actual
dependencies. Design decisions record how particular sources relate to this implementation.

## Apple and protocol sources

| Source | Use |
|---|---|
| [Device Management](https://developer.apple.com/documentation/devicemanagement) | MDM commands, check-in and enrollment protocol |
| [Account-driven enrollment](https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment) | Discovery, account authentication and platform/channel bearer rules |
| [Account-driven enrollment methods](https://support.apple.com/en-ca/guide/deployment/dep4d9e9cd26/web) | Account-driven Device Enrollment, account-driven User Enrollment and Managed Apple Account terminology |
| [Automated Device Enrollment](https://support.apple.com/guide/deployment/automated-device-enrollment-management-dep73069dd57/web) | Deployment terminology and enrollment requirements |
| [Integrating declarative management](https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management) | DDM within an MDM enrollment |
| [Device assignment](https://developer.apple.com/documentation/devicemanagement/device-assignment) | Device enrollment service APIs and `MachineInfo` |
| [Apple School and Business Manager APIs](https://developer.apple.com/documentation/apple-school-and-business-manager-api) | Service API documentation |
| [Device management schema](../../third_party/device-management/) | Pinned machine-readable wire definitions; provenance in `schema/GENERATED_FROM.json` |
| [RFC 8785](https://www.rfc-editor.org/rfc/rfc8785) | Canonical JSON |
| [RFC 8555](https://www.rfc-editor.org/rfc/rfc8555), [RFC 8894](https://www.rfc-editor.org/rfc/rfc8894) | ACME and SCEP |
| [RFC 5280](https://www.rfc-editor.org/rfc/rfc5280), [RFC 6960](https://www.rfc-editor.org/rfc/rfc6960) | Certificates, CRLs and OCSP |

The schema covers MDM check-in, commands, profiles and errors; declarative declarations,
status and protocol objects; and additional device management formats. Counts and platform
versions are derived from the pin, not maintained separately in this catalogue. Apple's source
descriptions remain verbatim in generated output.

## Repository index

`make refs` fetches the subset listed in [scripts/refs.sh](../../scripts/refs.sh) into
`third_party/refs` and records commits in its local `COMMITS.txt`. The script updates existing
reference checkouts; do not keep local edits there. Reference repositories are not imported
merely because they appear here.

| Review topic | Repository |
|---|---|
| Apple device management schema repository | [apple/device-management](https://github.com/apple/device-management) |
| Community schema for Apple web services | [micromdm/apple-device-services](https://github.com/micromdm/apple-device-services) |
| NanoMDM | [micromdm/nanomdm](https://github.com/micromdm/nanomdm) |
| NanoHUB | [micromdm/nanohub](https://github.com/micromdm/nanohub) |
| Fleet | [fleetdm/fleet](https://github.com/fleetdm/fleet) |
| MicroMDM | [micromdm/micromdm](https://github.com/micromdm/micromdm) |
| KMFDDM | [jessepeterson/kmfddm](https://github.com/jessepeterson/kmfddm) |
| MDMDirector | [mdmdirector/mdmdirector](https://github.com/mdmdirector/mdmdirector) |
| NanoCMD | [micromdm/nanocmd](https://github.com/micromdm/nanocmd) |
| Cairn-MDM | [nickpdawson/Cairn-MDM](https://github.com/nickpdawson/Cairn-MDM) |
| Local MDM | [Malcolm/local-mdm](https://github.com/Malcolm/local-mdm) |
| VEx | [roperzh/VEx](https://github.com/roperzh/VEx) |
| Zentral | [zentralopensource/zentral](https://github.com/zentralopensource/zentral) |
| Commandment | [cmdmnt/commandment](https://github.com/cmdmnt/commandment) |
| Micromanage | [liemeldert/Micromanage](https://github.com/liemeldert/Micromanage) |
| oreore-ios-mdm | [YusukeIwaki/oreore-ios-mdm](https://github.com/YusukeIwaki/oreore-ios-mdm) |
| apple-mdm-poc (Rust) | [maulanasdqn/apple-mdm-poc](https://github.com/maulanasdqn/apple-mdm-poc) |
| TDS MDM | [thomasdye12/TDSMDM](https://github.com/thomasdye12/TDSMDM) |
| apple-mdm-system | [JieAnthony/apple-mdm-system](https://github.com/JieAnthony/apple-mdm-system) |
| nanomdm-ui, Apple-Dep, CDMH-Scep | [cidumh/nanomdm-ui](https://github.com/cidumh/nanomdm-ui) |
| nanomdm-ui, Apple-Dep, CDMH-Scep | [cidumh/Apple-Dep](https://github.com/cidumh/Apple-Dep) |
| nanomdm-ui, Apple-Dep, CDMH-Scep | [cidumh/CDMH-Scep](https://github.com/cidumh/CDMH-Scep) |
| go-adm | [korylprince/go-adm](https://github.com/korylprince/go-adm) |
| mdmcommands and admgen | [jessepeterson/mdmcommands](https://github.com/jessepeterson/mdmcommands) |
| mdmcommands and admgen | [jessepeterson/admgen](https://github.com/jessepeterson/admgen) |
| go-sdk-appleservices | [deploymenttheory/go-sdk-appleservices](https://github.com/deploymenttheory/go-sdk-appleservices) |
| Contour | [macadmins/contour](https://github.com/macadmins/contour) |
| mobileconfig-builder | [dantecatalfamo/mobileconfig-builder](https://github.com/dantecatalfamo/mobileconfig-builder) |
| Sample declarations and deployment kits | [macadmins/ddm_examples](https://github.com/macadmins/ddm_examples) |
| Sample declarations and deployment kits | [macadmins/ddm_infra](https://github.com/macadmins/ddm_infra) |
| Sample declarations and deployment kits | [openmac-org/nanohub-acme-docker](https://github.com/openmac-org/nanohub-acme-docker) |
| Sample declarations and deployment kits | [openmac-org/nanomdm-acme-docker](https://github.com/openmac-org/nanomdm-acme-docker) |
| Sample declarations and deployment kits | [openmac-org/nanomdm-scep-docker](https://github.com/openmac-org/nanomdm-scep-docker) |
| DDM tools that are not open source implementations | [Jamf-Concepts/ddm-explorer](https://github.com/Jamf-Concepts/ddm-explorer) |
| DDM tools that are not open source implementations | [huexley/DDMStatus](https://github.com/huexley/DDMStatus) |
| DDM tools that are not open source implementations | [dan-snelson/DDM-OS-Reminder](https://github.com/dan-snelson/DDM-OS-Reminder) |
| nanolib | [micromdm/nanolib](https://github.com/micromdm/nanolib) |
| go4 | [micromdm/go4](https://github.com/micromdm/go4) |
| mdmutil | [micromdm/mdmutil](https://github.com/micromdm/mdmutil) |
| cfgprofiles | [jessepeterson/cfgprofiles](https://github.com/jessepeterson/cfgprofiles) |
| micro2nano | [micromdm/micro2nano](https://github.com/micromdm/micro2nano) |
| micromdm/plist (formerly groob/plist) | [micromdm/plist](https://github.com/micromdm/plist) |
| DHowett/go-plist (howett.net/plist) | [DHowett/go-plist](https://github.com/DHowett/go-plist) |
| smallstep/pkcs7 | [smallstep/pkcs7](https://github.com/smallstep/pkcs7) |
| mozilla-services/pkcs7 (go.mozilla.org/pkcs7) | [mozilla-services/pkcs7](https://github.com/mozilla-services/pkcs7) |
| fullsailor/pkcs7 | [fullsailor/pkcs7](https://github.com/fullsailor/pkcs7) |
| RobotsAndPencils/buford | [RobotsAndPencils/buford](https://github.com/RobotsAndPencils/buford) |
| sideshow/apns2 | [sideshow/apns2](https://github.com/sideshow/apns2) |
| Push certificate CSR tooling | [grinich/mdmvendorsign](https://github.com/grinich/mdmvendorsign) |
| Push certificate CSR tooling | [korylprince/fleetapns](https://github.com/korylprince/fleetapns) |
| Push certificate CSR tooling | [petarov/apns-push-cmd](https://github.com/petarov/apns-push-cmd) |
| smallstep/scep | [smallstep/scep](https://github.com/smallstep/scep) |
| micromdm/scep | [micromdm/scep](https://github.com/micromdm/scep) |
| smallstep/certificates (step-ca) | [smallstep/certificates](https://github.com/smallstep/certificates) |
| nanoca | [brandonweeks/nanoca](https://github.com/brandonweeks/nanoca) |
| mysqlscepserver | [jessepeterson/mysqlscepserver](https://github.com/jessepeterson/mysqlscepserver) |
| ios-acme-simulator | [hslatman/ios-acme-simulator](https://github.com/hslatman/ios-acme-simulator) |
| Non-Go SCEP (for interop testing) | [certnanny/sscep](https://github.com/certnanny/sscep) |
| Non-Go SCEP (for interop testing) | [mosen/SCEPy](https://github.com/mosen/SCEPy) |
| Non-Go SCEP (for interop testing) | [openxpki/openxpki](https://github.com/openxpki/openxpki) |
| Non-Go SCEP (for interop testing) | [Keyfactor/ejbca-ce](https://github.com/Keyfactor/ejbca-ce) |
| Non-Go SCEP (for interop testing) | [dogtagpki/pki](https://github.com/dogtagpki/pki) |
| NanoDEP | [micromdm/nanodep](https://github.com/micromdm/nanodep) |
| NanoAXM | [micromdm/nanoaxm](https://github.com/micromdm/nanoaxm) |
| dep-webview-oidc | [korylprince/dep-webview-oidc](https://github.com/korylprince/dep-webview-oidc) |
| Apple-JSON-discovery-server | [vbnin/Apple-JSON-discovery-server](https://github.com/vbnin/Apple-JSON-discovery-server) |
| Other ABM/ASM clients | [hitoshiichikawa/apple-business-go](https://github.com/hitoshiichikawa/apple-business-go) |
| Other ABM/ASM clients | [neilmartin83/terraform-provider-axm](https://github.com/neilmartin83/terraform-provider-axm) |
| Other ABM/ASM clients | [petarov/apple-mdm-clients](https://github.com/petarov/apple-mdm-clients) |
| ProfileManifests | [ProfileManifests/ProfileManifests](https://github.com/ProfileManifests/ProfileManifests) |
| ProfileCreator | [ProfileCreator/ProfileCreator](https://github.com/ProfileCreator/ProfileCreator) |
| mobileconfig-signer | [hslatman/mobileconfig-signer](https://github.com/hslatman/mobileconfig-signer) |
| Related | [deploymenttheory/go-settings-catalog](https://github.com/deploymenttheory/go-settings-catalog) |
| mdmb | [jessepeterson/mdmb](https://github.com/jessepeterson/mdmb) |
| Local development harnesses | [sheshenia/nanostarter](https://github.com/sheshenia/nanostarter) |
| Local development harnesses | [discentem/nanomdmsandbox](https://github.com/discentem/nanomdmsandbox) |
| Local development harnesses | [korylprince/kmfddm-docker](https://github.com/korylprince/kmfddm-docker) |
| imdmtools (Intrepidus Group) | [intrepidusgroup/imdmtools](https://github.com/intrepidusgroup/imdmtools) |
| Apple-iOS-MDM-Server | [vineetchoudhary/Apple-iOS-MDM-Server](https://github.com/vineetchoudhary/Apple-iOS-MDM-Server) |
| IOS-MDM-Server (Java) | [zuoyy/IOS-MDM-Server](https://github.com/zuoyy/IOS-MDM-Server) |
| activeMDM | [abstractec/activeMDM](https://github.com/abstractec/activeMDM) |
| WSO2 IoT Server | [wso2/product-iots](https://github.com/wso2/product-iots) |
| MicroMDM precursors | [micromdm/mdm](https://github.com/micromdm/mdm) |
| MicroMDM precursors | [micromdm/dep](https://github.com/micromdm/dep) |
| MicroMDM precursors | [micromdm/tools](https://github.com/micromdm/tools) |
| Others | [emersion/go-apple-mobileconfig](https://github.com/emersion/go-apple-mobileconfig) |
| Others | [nolanbrown/ios-cert-enrollment](https://github.com/nolanbrown/ios-cert-enrollment) |
| Others | [korylprince/macos-device-attestation](https://github.com/korylprince/macos-device-attestation) |

## Pinned implementation references

These commits provide reproducible references for enrollment association, admission and
certificate status design. They are provenance, not a statement about current upstream behavior.
Other decision records retain their relevant source pins and paths.

| Source | Commit | Review topic |
|---|---|---|
| [Zentral](https://github.com/zentralopensource/zentral/tree/c7947511a1f7323d61866e4cfe9591e88119ea7d) | `c7947511a1f7323d61866e4cfe9591e88119ea7d` | Enrollment sessions and certificate association |
| [Fleet](https://github.com/fleetdm/fleet/tree/4877c4f02d46c2775b93a91f4d45e1d5afe590a4) | `4877c4f02d46c2775b93a91f4d45e1d5afe590a4` | Optional ownership admission, bearer association and quotas |
| [MicroMDM](https://github.com/micromdm/micromdm/tree/904493b9500ffc8a21846846781e362f5c612107) | `904493b9500ffc8a21846846781e362f5c612107` | Certificate depot and pin admission |
