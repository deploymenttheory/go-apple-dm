# Changelog

## [0.8.3](https://github.com/deploymenttheory/go-apple-dm/compare/v0.8.2...v0.8.3) (2026-09-25)


### Features

* lab acceptance harness ([4b33998](https://github.com/deploymenttheory/go-apple-dm/commit/4b339982ee83008de32bc218d5bc81528be1e59c))
* **lab:** replace the bench with a public acceptance lab ([b6417cf](https://github.com/deploymenttheory/go-apple-dm/commit/b6417cf8160d6e7ae162154871383452e3082438))


### Bug Fixes

* **lab:** range-check the filesystem block size ([0894ec4](https://github.com/deploymenttheory/go-apple-dm/commit/0894ec4030790f4399620f239da8c0f19036ed69))
* **lab:** report a missing push identity instead of enrolling unreachable devices ([b275d24](https://github.com/deploymenttheory/go-apple-dm/commit/b275d24dd926667f3b7beac59aba003bd1c77775))
* **lab:** report a missing push identity instead of enrolling unreachable devices ([8a85ef5](https://github.com/deploymenttheory/go-apple-dm/commit/8a85ef5e43bdb89a4cf6b2c5e5eee4c2b7d2af09))
* **server:** pin library revision with the enrollment-link event ([7a4431a](https://github.com/deploymenttheory/go-apple-dm/commit/7a4431aad10745bfcfc821d7edbebff53e82a469))
* **sqlstore:** keep a cancellation identifiable when BEGIN is interrupted ([#262](https://github.com/deploymenttheory/go-apple-dm/issues/262)) ([ff811ca](https://github.com/deploymenttheory/go-apple-dm/commit/ff811cad7da4cc75ddf5ca8763acee803488b0c2))


### Refactoring

* **pki:** name managed certificate identities for what they are ([#259](https://github.com/deploymenttheory/go-apple-dm/issues/259)) ([ad80884](https://github.com/deploymenttheory/go-apple-dm/commit/ad80884cd36496bdbe67204ae845be977b8b30e1))

## [0.8.2](https://github.com/deploymenttheory/go-apple-dm/compare/v0.8.1...v0.8.2) (2026-09-22)


### Features

* **inventory:** add agentless Apple device records ([e70178c](https://github.com/deploymenttheory/go-apple-dm/commit/e70178c6cb29c46c6337dfc3451105b4a6286996))
* **inventory:** add agentless Apple device records ([749dbcd](https://github.com/deploymenttheory/go-apple-dm/commit/749dbcdecbf6af586b8667b3aa3fb4362dbb8af3))


### Bug Fixes

* **inventory:** preserve native evidence and omit empty values ([4b7c4ae](https://github.com/deploymenttheory/go-apple-dm/commit/4b7c4aee8558e518862d1e841fb2d7f0049c0a9c))
* **server:** pin library revision with agentless inventory ([5467df9](https://github.com/deploymenttheory/go-apple-dm/commit/5467df921145a6e12bc30bc0f05e93ae3d2033d2))
* **server:** ship verified inventory normalization fixes ([a8f21ee](https://github.com/deploymenttheory/go-apple-dm/commit/a8f21eeb1dc55336d714eeb142f8bfadeb70c41c))


### Documentation

* **cli:** expose native inventory guidance in verb help ([271d726](https://github.com/deploymenttheory/go-apple-dm/commit/271d726e9cb4929e3b3aed9a933b00985ff7ea7f))
* **diagrams:** add five bench review options ([970b47e](https://github.com/deploymenttheory/go-apple-dm/commit/970b47e019ea4dd5c7694331e4225ebfc5c2a952))
* **diagrams:** adopt system map for reference server bench ([92a720a](https://github.com/deploymenttheory/go-apple-dm/commit/92a720a1006c760c08a86c6b5f3f75f6fdac7169))
* **diagrams:** adopt system map for reference server bench ([507bd94](https://github.com/deploymenttheory/go-apple-dm/commit/507bd94953f15081f84f008e9ff5205c3247b05f))
* **diagrams:** adopt system map for reference server bench ([d087153](https://github.com/deploymenttheory/go-apple-dm/commit/d087153aa18a43aaedaf02d1745dd0b93c0b873e))
* **inventory:** explain collection fields and check-in flow ([2ab189c](https://github.com/deploymenttheory/go-apple-dm/commit/2ab189cb23efcbddbc3c8c5ed148d94015d6b007))

## [0.8.1](https://github.com/deploymenttheory/go-apple-dm/compare/v0.8.0...v0.8.1) (2026-09-21)


### Bug Fixes

* correct DDM delivery, DEP workers, and admin responses ([9c2d100](https://github.com/deploymenttheory/go-apple-dm/commit/9c2d1008436c54a033d4895c701fc545408cf9d1))
* correct lifecycle and DEP behavior and reconcile diagrams ([3b4949f](https://github.com/deploymenttheory/go-apple-dm/commit/3b4949f6ed33aa5929ee48d05302aa4535b485e6))
* **ddm:** commit lifecycle cleanup with authoritative authentication outcomes ([4c7bc4a](https://github.com/deploymenttheory/go-apple-dm/commit/4c7bc4a9e634a94da89630d248a64d55c8e0ff6d))
* **dep:** fence account changes and reset forced identity replacements atomically ([55218ec](https://github.com/deploymenttheory/go-apple-dm/commit/55218ecb526cfcf77607dac52a8b48bfdafb7393))
* **depstore:** serialize state writes without locking absent account rows ([c1a9487](https://github.com/deploymenttheory/go-apple-dm/commit/c1a9487d510ddbad038e8a171861a4f96ebfd5dd))
* **docs:** reject links to untracked local targets ([ae50a83](https://github.com/deploymenttheory/go-apple-dm/commit/ae50a8307418380b0a26d955fbeb7762f5dff438))
* preserve device sync state and enforce API response boundaries ([8eceb6f](https://github.com/deploymenttheory/go-apple-dm/commit/8eceb6f43592cb77839d52b2e32454c064d12f39))
* **runtime:** drain observed writes and retain resources after shutdown timeouts ([e3db044](https://github.com/deploymenttheory/go-apple-dm/commit/e3db0449b366f96ebb0d6e27a02a1d65d382e1a5))


### Documentation

* draw incomplete shutdown with a direct arrow ([964fbfa](https://github.com/deploymenttheory/go-apple-dm/commit/964fbfa8db817789d95ec97e3ddaefd2efafcf11))
* draw incomplete shutdown with a direct arrow ([793e24b](https://github.com/deploymenttheory/go-apple-dm/commit/793e24b994075f804a17c238ab817ee45bf704f0))
* reconcile audited diagrams with implementation and vendor contracts ([3810b8f](https://github.com/deploymenttheory/go-apple-dm/commit/3810b8fb00dc19cf298df5bd125e748003d09e33))
* reconcile documentation and diagrams with current behavior ([dde3a4d](https://github.com/deploymenttheory/go-apple-dm/commit/dde3a4df649be09795788d1c8eaf62ba6dedd4c0))
* reconcile documentation and diagrams with current behavior ([812ac0e](https://github.com/deploymenttheory/go-apple-dm/commit/812ac0ef205e9e9e3495f8487a403adf23ddb5f4))
* refresh diagram counts and catalogue summaries ([01aba00](https://github.com/deploymenttheory/go-apple-dm/commit/01aba0072fa30aa4e4b97dcd753af8433c72546b))
* refresh diagram counts and catalogue summaries ([3e465cf](https://github.com/deploymenttheory/go-apple-dm/commit/3e465cfd56a13f7d85957700aa93c7389f30a8d7))

## [0.8.0](https://github.com/deploymenttheory/go-apple-dm/compare/v0.7.4...v0.8.0) (2026-09-20)


### ⚠ BREAKING CHANGES

* **server:** DM_WEBHOOK_URL and DM_WEBHOOK_HMAC_KEY now require migration to managed subscriptions.

### Features

* **server:** add managed native webhooks ([4e56864](https://github.com/deploymenttheory/go-apple-dm/commit/4e56864d425ccf72a2fbe6bbc47d7beeef5fe83a))
* **server:** add managed native webhooks ([31a75a9](https://github.com/deploymenttheory/go-apple-dm/commit/31a75a95e767789bd70d2fbf352af1ce587fab7c))
* **server:** implement unified device management RBAC ([0d135cd](https://github.com/deploymenttheory/go-apple-dm/commit/0d135cdfc31999079c175e3c86ef51842718d661))
* **server:** implement unified device management RBAC ([790c778](https://github.com/deploymenttheory/go-apple-dm/commit/790c77868b069972f159a62cec72f48d7df0f003))


### Bug Fixes

* **server:** preserve captured payloads across webhook replays ([02788ea](https://github.com/deploymenttheory/go-apple-dm/commit/02788ea70260d7165925d2056b64371c6259ed6e))

## [0.7.4](https://github.com/deploymenttheory/go-apple-dm/compare/v0.7.3...v0.7.4) (2026-09-20)


### Features

* add Blueprint composition and DDM publication ([45f8e5a](https://github.com/deploymenttheory/go-apple-dm/commit/45f8e5a28562c72144487b212250536b1fe04370))
* add Blueprint composition and DDM publication ([45f09cb](https://github.com/deploymenttheory/go-apple-dm/commit/45f09cb1dd77c5c8433a9b3ef71a3220759d7706))
* add guided onboarding and local Compose quickstart ([10058fe](https://github.com/deploymenttheory/go-apple-dm/commit/10058febcaea9795952fa2fd99111be429093706))
* add guided onboarding and local Compose quickstart ([f84a831](https://github.com/deploymenttheory/go-apple-dm/commit/f84a831509fbc9d88f7810866a0b49fbe97b2d3e))
* add macOS 27 compatibility and shared OS version support ([ee64353](https://github.com/deploymenttheory/go-apple-dm/commit/ee643534df5769ff54303fdc491ba75bff9339b6))
* add macOS 27 compatibility and shared OS version support ([69adcf4](https://github.com/deploymenttheory/go-apple-dm/commit/69adcf428a64368c505f6cb94807a9da25922516))
* **dmctl:** add native application identity discovery and target validation ([b373a13](https://github.com/deploymenttheory/go-apple-dm/commit/b373a139de21f8cf5c4adb4b194202fba696ebff))
* **server:** discover application identities for DDM authoring ([a7026bf](https://github.com/deploymenttheory/go-apple-dm/commit/a7026bfaab6b0da45ab189ac31b1b855be22089b))
* **server:** expose application identity discovery for DDM authoring ([a5d404f](https://github.com/deploymenttheory/go-apple-dm/commit/a5d404f8a6059038864fc6a6c9b1ecabf50e4be0))
* **utility:** add app identity discovery and App Settings builders ([646da4e](https://github.com/deploymenttheory/go-apple-dm/commit/646da4efe9d4d31d710b5bb519a9ebfbbef98ab4))
* **utility:** discover app identities for DDM authoring ([77a660e](https://github.com/deploymenttheory/go-apple-dm/commit/77a660e2682a86660556b86fc0529e5503c38d7f))
* **utility:** discover application identities in portable artifacts ([68907f4](https://github.com/deploymenttheory/go-apple-dm/commit/68907f4c720ac12bcb30e9bd29c2cc95af389e19))


### Bug Fixes

* **ci:** restore monitor artifact paths ([a43ba55](https://github.com/deploymenttheory/go-apple-dm/commit/a43ba55b97e01e8a700fdf6f32e2799bb276200d))
* **ci:** restore monitor assessment artifact path ([50d077a](https://github.com/deploymenttheory/go-apple-dm/commit/50d077a427e67828d54b285c4749d4ea8e764dc0))
* **ci:** restore monitor assessment artifact path ([58cc822](https://github.com/deploymenttheory/go-apple-dm/commit/58cc822184c3214a898a4ffeca23bebd486b8e1c))
* **ci:** restore schema monitor artifact paths ([7999efb](https://github.com/deploymenttheory/go-apple-dm/commit/7999efb2704c4a40446747d0a5a2e9f956190513))
* **ci:** scope schema monitoring to future code generation ([051caa5](https://github.com/deploymenttheory/go-apple-dm/commit/051caa57a9fbef5a1cf69a7e95c38147d1090e51))
* **ci:** scope schema monitoring to future code generation ([e49e10a](https://github.com/deploymenttheory/go-apple-dm/commit/e49e10ae4e0c760e6284e9efa194814b8645320c))
* complete macOS 27 library and server validation ([ebceb75](https://github.com/deploymenttheory/go-apple-dm/commit/ebceb75c9a28a9d20365a2fd4112fd1244a4dbb9))
* preserve cancellation during transaction shutdown ([767197e](https://github.com/deploymenttheory/go-apple-dm/commit/767197ee17f29763869c6e63ce8d9b253a446076))
* **schema:** fetch retained assessment snapshots ([ad7ad74](https://github.com/deploymenttheory/go-apple-dm/commit/ad7ad748799e6eb8bbf3e5203f6b0161a1690929))
* **schema:** permit generated package documentation ([3f4ad32](https://github.com/deploymenttheory/go-apple-dm/commit/3f4ad329f12ad7e9901270eb8c5db50a8b565216))
* **schema:** permit generated package documentation ([3819de8](https://github.com/deploymenttheory/go-apple-dm/commit/3819de8063859a9f707e82379799cddc10a7cfaa))
* **schema:** replay each retained snapshot transition ([fe5755a](https://github.com/deploymenttheory/go-apple-dm/commit/fe5755ac95ece7d2ea67c721fb54f6a671065f4d))
* **schema:** replay each retained snapshot transition ([9305e3d](https://github.com/deploymenttheory/go-apple-dm/commit/9305e3d471016cfdb51013adf19102ff379337e4))
* **schema:** report adjacent journey versions ([f93b3ba](https://github.com/deploymenttheory/go-apple-dm/commit/f93b3ba758461f2fce116e058d2fd9847e119281))
* **schema:** separate release and canary sources ([b79da48](https://github.com/deploymenttheory/go-apple-dm/commit/b79da486548d1c041b7b0a6e746f383119648cbf))
* **schema:** separate release and canary sources ([89d8829](https://github.com/deploymenttheory/go-apple-dm/commit/89d88298eaa0c86de11f85c87358c7a566efdfe7))
* **server:** pin library revision containing Blueprint APIs ([1706d74](https://github.com/deploymenttheory/go-apple-dm/commit/1706d74283510fb46edf8a56e85e3001d6bfb370))
* share persistence across split MDM and DDM tests ([617119b](https://github.com/deploymenttheory/go-apple-dm/commit/617119b0b66a3c66cfb4e88ba78a134eb4e5234d))
* validate binary policy identities and record macOS 27 findings ([cddf78c](https://github.com/deploymenttheory/go-apple-dm/commit/cddf78c3196a9ba561af4d079534e548f05a3186))


### Refactoring

* make osversion the sole version API ([c81508c](https://github.com/deploymenttheory/go-apple-dm/commit/c81508cf336c671c9fa50ee02ca8b36262ce255e))
* **schema:** version third-party sources ([652bb90](https://github.com/deploymenttheory/go-apple-dm/commit/652bb907635907e3c3529b84305dfbe7bdb31b30))
* **schema:** version third-party sources ([b3ccea9](https://github.com/deploymenttheory/go-apple-dm/commit/b3ccea970c0d9a6bbd6eaece8079d62a27b4ef98))
* **utility:** focus on app identity discovery for DDM authoring ([63fe182](https://github.com/deploymenttheory/go-apple-dm/commit/63fe1822fe37c606d8d09163c53892cc37122877))


### Documentation

* clarify macOS binary-control signing requirements ([8d35cc9](https://github.com/deploymenttheory/go-apple-dm/commit/8d35cc9fc1e06a310442e1b1e53570dbc437dc4a))
* clarify macOS binary-control signing requirements ([1a0e5ef](https://github.com/deploymenttheory/go-apple-dm/commit/1a0e5ef3754bd6d9337358fa4986168ecd77eef5))
* record macOS 27 guest enrollment blocker and CI results ([c582bb7](https://github.com/deploymenttheory/go-apple-dm/commit/c582bb7d09a0c136777999802c86dfec4a087c6f))
* record native provisioning enrollment retry ([97b4d35](https://github.com/deploymenttheory/go-apple-dm/commit/97b4d3508b4d5ec69860abbbad1602cd7858e2b9))
* **testing:** record installed application identity acceptance ([626c918](https://github.com/deploymenttheory/go-apple-dm/commit/626c918bc558a29054d0b7f6a72d98502e9f01c5))
* **testing:** record physical app-control isolation failure ([c4657fc](https://github.com/deploymenttheory/go-apple-dm/commit/c4657fc6b883823fec1f1c57c37e23f514474344))

## [0.7.3](https://github.com/deploymenttheory/go-apple-dm/compare/v0.7.2...v0.7.3) (2026-09-16)


### Features

* add Apple management helpers and Apps and Books licensing ([8ef73c9](https://github.com/deploymenttheory/go-apple-dm/commit/8ef73c9a13ce487627518224249db02db87405f7))
* add Apple management helpers and Apps and Books licensing ([7500eb2](https://github.com/deploymenttheory/go-apple-dm/commit/7500eb25e50a2913338339d5fced18dc7f41fdc3))


### Bug Fixes

* release please ([6da7312](https://github.com/deploymenttheory/go-apple-dm/commit/6da7312b17b4452dc77530f59499760780a6637f))


### Documentation

* reconcile implementation guides and design decisions ([b071214](https://github.com/deploymenttheory/go-apple-dm/commit/b0712144c94e445037f4c5629cc987597863ecb2))

## [0.7.2](https://github.com/deploymenttheory/go-apple-dm/compare/v0.7.1...v0.7.2) (2026-09-14)


### Bug Fixes

* align scope documentation and stabilize CI ([5a658aa](https://github.com/deploymenttheory/go-apple-dm/commit/5a658aa0bcfbdf24e2261ff8f9931f9dc955ba63))
* drain bench supervisor and remove duplicate CI work ([f061877](https://github.com/deploymenttheory/go-apple-dm/commit/f0618775dd3261c25781d746ad75b8efeb3052eb))
* retain embedded bench listeners through runtime startup ([3440eb0](https://github.com/deploymenttheory/go-apple-dm/commit/3440eb0a34c90464241853dedfb2d27f807dc115))


### Documentation

* align private proxy and identity renewal contracts ([6142fa2](https://github.com/deploymenttheory/go-apple-dm/commit/6142fa24974bfea1625f0a6f6104b5bf5cd9bf97))
* constrain extension proposals and align implementation guides ([d76488e](https://github.com/deploymenttheory/go-apple-dm/commit/d76488e0bb64e657c1bef95f90e534472e29331c))
* expand diagram coverage and refresh validation guidance ([263ba3d](https://github.com/deploymenttheory/go-apple-dm/commit/263ba3de5540206ddb295d52625737c4651cf0fa))
* preserve links to historical validation evidence ([0317981](https://github.com/deploymenttheory/go-apple-dm/commit/0317981799b756d4d588e53f95c1e264837fb72d))
* tidy event delivery guidance ([458033e](https://github.com/deploymenttheory/go-apple-dm/commit/458033e0c08743a61e5eb3cac1bc3f53120ed79f))

## [0.7.1](https://github.com/deploymenttheory/go-apple-dm/compare/v0.7.0...v0.7.1) (2026-09-14)


### Features

* **server:** package server and CLI release assets ([b88a2ca](https://github.com/deploymenttheory/go-apple-dm/commit/b88a2ca9dce39cd6fdc67a7312053a26da3cdf1f))


### Bug Fixes

* **ci:** correct Windows permissions, line endings and layout checks ([f29baf1](https://github.com/deploymenttheory/go-apple-dm/commit/f29baf116e0cc45ccb140fddaffed845c001e905))
* **enroll:** preserve manual macOS installing-user scope and verify its channel ([b1a0d19](https://github.com/deploymenttheory/go-apple-dm/commit/b1a0d19da5dc45282f95b72651db945ffa218eb4))
* **server:** add fenced backup verification and restore commands ([24e42f3](https://github.com/deploymenttheory/go-apple-dm/commit/24e42f3315307153972b044dee7ca1169e1af699))
* **server:** complete Mac lifecycle, recovery and release workflows ([80ea482](https://github.com/deploymenttheory/go-apple-dm/commit/80ea482c29044503c953dd30044aeb49825245ed))
* **server:** pin reviewed library and add authenticated recovery archives ([b9f3330](https://github.com/deploymenttheory/go-apple-dm/commit/b9f3330de2c1cfced8404d829e4da6824d1f64cf))
* **server:** preserve Mac lifecycle and record events in persistent state ([6629481](https://github.com/deploymenttheory/go-apple-dm/commit/66294811b361d0f30f838acabfa9101351263651))
* **server:** support Windows private files and native process tests ([213e9ae](https://github.com/deploymenttheory/go-apple-dm/commit/213e9aecb377bcdf136e5041f41b606f4d635cad))
* **server:** validate recovery cursors and cover failure boundaries ([1c8ecba](https://github.com/deploymenttheory/go-apple-dm/commit/1c8ecba1a436bc7b99f145f522958d20e5779439))


### Documentation

* add missing package documentation across both modules ([6c1b70f](https://github.com/deploymenttheory/go-apple-dm/commit/6c1b70fc0f5f556a6cc6287caa5e8cdb4a2ae386))

## [0.7.0](https://github.com/deploymenttheory/go-apple-dm/compare/v0.6.0...v0.7.0) (2026-09-13)


### Features

* **pki:** add managed certificate setup and renewal ([6fd5a46](https://github.com/deploymenttheory/go-apple-dm/commit/6fd5a46be7b806eab0dd7ec6423d2320a66224a6))
* **pki:** manage certificate setup and renewal with persistent state ([5d23ceb](https://github.com/deploymenttheory/go-apple-dm/commit/5d23ceb13acb96e27a29b1a8075360a45375329b))


### Bug Fixes

* **server:** address certificate setup security scan findings ([c5b7eb4](https://github.com/deploymenttheory/go-apple-dm/commit/c5b7eb4819effa9ee670b4d76a97b1aa65ab1557))
* **server:** pin published certificate lifecycle library ([bcbae9a](https://github.com/deploymenttheory/go-apple-dm/commit/bcbae9ac67789214b631abdde3947f793d4f7903))
* **server:** verify certificate workflows and restore coverage gate ([9dba169](https://github.com/deploymenttheory/go-apple-dm/commit/9dba169e546f0f858b048483931703c5c145b10d))

## [0.6.0](https://github.com/deploymenttheory/go-apple-dm/compare/v0.5.0...v0.6.0) (2026-09-12)


### Features

* add OS 27 support while preserving mixed-fleet compatibility ([324fb2d](https://github.com/deploymenttheory/go-apple-dm/commit/324fb2dc07736e38c5ca3b755cc413e1d6010a8a))
* prepare OS 27 compatibility and content-cache ingestion ([8395624](https://github.com/deploymenttheory/go-apple-dm/commit/8395624c4ab8f3d0339cbe6deb2391ab1634a938))
* **schema:** adopt OS 27 while preserving historical contracts ([7826a58](https://github.com/deploymenttheory/go-apple-dm/commit/7826a58d45163ea73cdca412fdbe79938e173ed8))


### Bug Fixes

* preserve management across mixed OS fleet versions ([3ea1fb4](https://github.com/deploymenttheory/go-apple-dm/commit/3ea1fb4208dba1ef9916c7a6081086a4aed9b8e4))
* **schema:** retain accurate promotion assessment provenance ([6601f08](https://github.com/deploymenttheory/go-apple-dm/commit/6601f08e3682430efc8a39e9dffc5c0ebc9e61bb))
* **server:** pin library with mixed-fleet storage extensions ([3b66660](https://github.com/deploymenttheory/go-apple-dm/commit/3b66660c46f52314ae1c7f957a50b9e858de361b)), closes [#40](https://github.com/deploymenttheory/go-apple-dm/issues/40)
* **server:** use published OS 27 library and record validation ([83db1aa](https://github.com/deploymenttheory/go-apple-dm/commit/83db1aa4ee91117b367dbd315684cc8e35387f81))
* supply required queries in bench commands ([78938fa](https://github.com/deploymenttheory/go-apple-dm/commit/78938fa9902236d87ff6e4aeeeb48a9d70121d91))


### Documentation

* record schema incident remediation and validation ([280ae72](https://github.com/deploymenttheory/go-apple-dm/commit/280ae722e2b3fa7ab49b3e328d08607533d2f77f))

## [0.5.0](https://github.com/deploymenttheory/go-apple-dm/compare/v0.4.0...v0.5.0) (2026-09-12)


### ⚠ BREAKING CHANGES

* move library packages under devicemanagement

### Features

* **schemagen:** monitor Apple stable and seed compatibility ([5b35e09](https://github.com/deploymenttheory/go-apple-dm/commit/5b35e099f49461c3cdb4bdc14ee9f7cc5f3256d5))
* **schemagen:** monitor stable and seed compatibility ([ac5561d](https://github.com/deploymenttheory/go-apple-dm/commit/ac5561d5551935a7a01d78a1350e78d1dccdda05))


### Bug Fixes

* **schemagen:** resolve candidate workspace paths before testing ([164db9d](https://github.com/deploymenttheory/go-apple-dm/commit/164db9dbc2d0cd03dd77336ef6d41255b0323c69))
* **schemagen:** retain independent protocol test evidence ([63c5458](https://github.com/deploymenttheory/go-apple-dm/commit/63c54586d0111b4bd92b47ad329aa5f78cb061f1))


### Refactoring

* move library packages under devicemanagement ([3014128](https://github.com/deploymenttheory/go-apple-dm/commit/30141285a2a50d6dbaf6d003973e41acc52ab13a))
* move library packages under devicemanagement ([bb9e5bb](https://github.com/deploymenttheory/go-apple-dm/commit/bb9e5bb3af89683a960a326e309be409bb0dc40f))
* rename schema generator command to schemagen ([9c5bd3f](https://github.com/deploymenttheory/go-apple-dm/commit/9c5bd3f90af69312da1c719638bf80c2f320ab00))
* **server:** adopt devicemanagement library imports ([e519b28](https://github.com/deploymenttheory/go-apple-dm/commit/e519b286bc1b66e498d104743cf7581e68588d88))

## [0.4.0](https://github.com/deploymenttheory/go-apple-dm/compare/v0.3.3...v0.4.0) (2026-09-12)


### Features

* **event:** bound asynchronous delivery and report overload ([816e0f2](https://github.com/deploymenttheory/go-apple-dm/commit/816e0f2370fbbcd2727fb4d1e9dde2333b7b8abf))


### Bug Fixes

* harden ACME issuance and reference-server security ([b241388](https://github.com/deploymenttheory/go-apple-dm/commit/b241388c35f5d06ee11011b22bef529e2b558d86))
* harden ACME issuance and reference-server security ([1f3614e](https://github.com/deploymenttheory/go-apple-dm/commit/1f3614e7a5db3c08aa487ebc580f55fcdea83a46))
* resolve event accounting and TLS probe gosec findings ([3c3cd7d](https://github.com/deploymenttheory/go-apple-dm/commit/3c3cd7dbba3d04fe2691510c963f675c55d4321b))
* resolve module installation, event delivery and health-check defects ([c4cb96f](https://github.com/deploymenttheory/go-apple-dm/commit/c4cb96f6b5a7d6ad7cbb18b66e7e3368e1bc1d9c))
* **security:** harden Apple PKI issuance and credential handling ([59d4636](https://github.com/deploymenttheory/go-apple-dm/commit/59d4636d573b65fc129ac31638af24efb373abb2))
* **security:** harden Apple PKI issuance and credential handling ([35e1783](https://github.com/deploymenttheory/go-apple-dm/commit/35e17835b40b2a71d4f1b709e43a239c6982dc5d))
* **server:** resolve project review integration and reliability defects ([a3cb252](https://github.com/deploymenttheory/go-apple-dm/commit/a3cb25246ebad35090a99c704f334ec2a09b906f))


### Documentation

* add comprehensive getting-started guide ([538e709](https://github.com/deploymenttheory/go-apple-dm/commit/538e709ed3fb499cb2178edf06d8cf87f4eec2c4))
* add comprehensive getting-started guide ([8c1d412](https://github.com/deploymenttheory/go-apple-dm/commit/8c1d4124802c06c903c2e9a1ea7284c6cde59f5f))

## [0.3.3](https://github.com/deploymenttheory/go-apple-dm/compare/v0.3.2...v0.3.3) (2026-09-10)


### Bug Fixes

* **security:** harden SCEP and Apple enrollment conformance ([cdad791](https://github.com/deploymenttheory/go-apple-dm/commit/cdad7916599dd26e00aba3f4ff4e65db04f10bbc))
* **security:** harden SCEP and Apple enrollment conformance ([e3eb3b5](https://github.com/deploymenttheory/go-apple-dm/commit/e3eb3b565717fd2c60f4c04b0f59eca3084f038d))

## [0.3.2](https://github.com/deploymenttheory/go-apple-dm/compare/v0.3.1...v0.3.2) (2026-09-10)


### Bug Fixes

* **security:** enforce enrollment trust boundaries ([4f5c793](https://github.com/deploymenttheory/go-apple-dm/commit/4f5c793fdf7ec8ca34f4895a8685577e010676f0))
* **security:** enforce enrollment trust boundaries ([0776e88](https://github.com/deploymenttheory/go-apple-dm/commit/0776e88a4a98b35f88e8997dff66bf573f25378c))

## [0.3.1](https://github.com/deploymenttheory/go-apple-dm/compare/v0.3.0...v0.3.1) (2026-09-10)


### Documentation

* adopt interactive purpose legends across diagrams ([bdb4e6b](https://github.com/deploymenttheory/go-apple-dm/commit/bdb4e6bb58904944822cc9ec380c576a08043cf6))
* align diagrams and add interactive purpose legends ([7552b87](https://github.com/deploymenttheory/go-apple-dm/commit/7552b87e02d5b02d2159860cfb17f1a7de880341))
* align diagrams with implementation and purpose colours ([5d9b59b](https://github.com/deploymenttheory/go-apple-dm/commit/5d9b59b08e0045db26fd094b67fd56245260ff13))
* open rendered diagrams from catalogue links ([e7e768f](https://github.com/deploymenttheory/go-apple-dm/commit/e7e768faabc90b730178896bb88488221de722ff))
* open rendered diagrams from catalogue links ([7484d07](https://github.com/deploymenttheory/go-apple-dm/commit/7484d078fbd5427ee7f5f3b65309f20f645934c0))

## [0.3.0](https://github.com/deploymenttheory/go-apple-dm/compare/v0.2.0...v0.3.0) (2026-09-10)


### Features

* align enrollment profiles and add controlled replacement ([91fd45e](https://github.com/deploymenttheory/go-apple-dm/commit/91fd45e2ced1405728284b6d1c9a3f3e7cf1fce2))
* align enrollment profiles and add controlled replacement ([eee03b3](https://github.com/deploymenttheory/go-apple-dm/commit/eee03b3799769977a9f13c9259b9dee57864a156))
* unify the reference-server bench and APNs certificate workflows ([9c3f4b1](https://github.com/deploymenttheory/go-apple-dm/commit/9c3f4b1717ff938cfa2a44ec7c2601b347d549c0))
* unify the reference-server bench and APNs certificate workflows ([6c7c452](https://github.com/deploymenttheory/go-apple-dm/commit/6c7c45290906efe12f0cf1c47a0c027627bf7647))

## [0.2.0](https://github.com/deploymenttheory/go-apple-dm/compare/v0.1.1...v0.2.0) (2026-09-07)


### Features

* enrollment authentication and optional security services ([b9900d8](https://github.com/deploymenttheory/go-apple-dm/commit/b9900d835fcb1b7f50e7e62df044ba5b7be28151))
* enrollment authentication and optional security services ([693a8f6](https://github.com/deploymenttheory/go-apple-dm/commit/693a8f646226c295a8935fc708d807dfe2cbeeff))


### Bug Fixes

* complete enrollment security hardening and validation ([c241aa8](https://github.com/deploymenttheory/go-apple-dm/commit/c241aa88f088c8225014bbf65d8c014dd6519a7f))


### Documentation

* prepare documentation and comments for v0.1.0 ([c8cba48](https://github.com/deploymenttheory/go-apple-dm/commit/c8cba48d466d8c98673288fabdd7c55cf7b1d333))
* prepare documentation and comments for v0.1.0 ([3841849](https://github.com/deploymenttheory/go-apple-dm/commit/38418493ef0a7b2c779933274196f589af542125))

## [0.1.1](https://github.com/deploymenttheory/go-apple-dm/compare/v0.1.0...v0.1.1) (2026-09-06)


### Bug Fixes

* **ci:** let release-please read the manifest ([4e13be3](https://github.com/deploymenttheory/go-apple-dm/commit/4e13be37dcec8d19a4ce920ba9d311876ebf177c))
