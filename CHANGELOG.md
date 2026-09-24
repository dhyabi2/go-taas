# [1.3.0](https://github.com/go-taas/go-taas/compare/v1.2.0...v1.3.0) (2026-09-24)


### Features

* **site:** add bilingual static intro site for GitHub Pages ([cea7734](https://github.com/go-taas/go-taas/commit/cea773457b603b9123a7fe60fe12e8f1b38c8fb8))

# [1.2.0](https://github.com/go-taas/go-taas/compare/v1.1.0...v1.2.0) (2026-09-24)


### Bug Fixes

* **metering:** map malformed voucher ids to 10403 instead of internal error ([95400dc](https://github.com/go-taas/go-taas/commit/95400dcffe8b6b8b6220be4815d3474bf77d9155))
* **web:** repair console routing and api-key list, polish compose workflow ([e6d803c](https://github.com/go-taas/go-taas/commit/e6d803c2c855707c24ab38f839bfc68d075e4ff4))


### Features

* **api,web:** split admin surface under /api/v1/admin and /admin console prefix ([da83572](https://github.com/go-taas/go-taas/commit/da83572ff244c13a2b547fd12d5e66739af105b8))
* **billing:** implement price matrix, tiered pricing and charging engine (feature-05) ([c17da8a](https://github.com/go-taas/go-taas/commit/c17da8a3a130d5c28ad781506a4a59c946b30960))
* **image:** add db-backed image registry and warmup pre-pull ([d7eaa31](https://github.com/go-taas/go-taas/commit/d7eaa315e2215406682320c81c836cc0dec041c9))
* **metering:** add token vouchers, hourly settlement and usage queries ([ca469a7](https://github.com/go-taas/go-taas/commit/ca469a71555278694ee215a11a37d932f428ebbf))

# [1.1.0](https://github.com/go-taas/go-taas/compare/v1.0.0...v1.1.0) (2026-09-23)


### Features

* **auth:** add API key lifecycle management ([b4f8f9d](https://github.com/go-taas/go-taas/commit/b4f8f9de77c5f1216d92c05887c4e6d903bac11e))
* **model,infer:** add model catalog and one-click deployment ([9da37a0](https://github.com/go-taas/go-taas/commit/9da37a08b57a5cbc1c9d3464c6fc58cb718f3579))
* **web:** add admin console for api keys, model catalog and inference services ([57969d4](https://github.com/go-taas/go-taas/commit/57969d4b33b1791ee7fdc06833b4d0e4c2ea3a95))

# 1.0.0 (2026-09-16)


### Bug Fixes

* address PR review build workflow issues ([802970f](https://github.com/go-taas/go-taas/commit/802970fff9e9efda56ef6d1e41479e43d1ff5292))


### Features

* scaffold Go codebase foundation ([701cab5](https://github.com/go-taas/go-taas/commit/701cab58226f7a7c3f3f95bc932d532bfeecf667))

# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Versions and entries below this notice are generated automatically by
semantic-release from Conventional Commits; do not edit them by hand.
