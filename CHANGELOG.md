# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Remove finalizers from reconcile logic.
- Ignore `skip` results.

## [0.0.5] - 2023-11-30

### Changed

- Keep `policy-exceptions` namespace when deleting the chart.
- Changed cleanup-job template to include `selector.labels`.

## [0.0.4] - 2023-11-29

### Added

- Add `Namespace` exclusion from Draft generation.
- Add `targetWorkloads` and `targetCategories` flags to allow Categories and Workload customization.
- Add `cleanup` Job when upgrading or deleting `exception-recommender`.

### Changed

- Change `PolicyExceptionDraftSpec` to `PolicyExceptionSpec`.
- Append `Kind` to `PolicyExceptionDraft` name.

## [0.0.3] - 2023-11-10

### Added

- Add CiliumNetworkPolicy.

## [0.0.2] - 2023-10-10

### Changed

- Run preinstall job as non-root.

## [0.0.1] - 2023-10-05

### Added

- First release of the Exception Recommender App.

[Unreleased]: https://github.com/giantswarm/exception-recommender/compare/v0.0.5...HEAD
[0.0.5]: https://github.com/giantswarm/exception-recommender/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/giantswarm/exception-recommender/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/giantswarm/exception-recommender/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/giantswarm/exception-recommender/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/giantswarm/exception-recommender/releases/tag/v0.0.1
