# Changelog

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
