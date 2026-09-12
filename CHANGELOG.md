# Changelog

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
