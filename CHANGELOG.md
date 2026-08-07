# Changelog

## [0.6.0](https://github.com/lcondliffe/gitling/compare/gitling-v0.5.1...gitling-v0.6.0) (2026-08-07)


### Features

* add --date=author|commit bucketing toggle ([#11](https://github.com/lcondliffe/gitling/issues/11)) ([045f117](https://github.com/lcondliffe/gitling/commit/045f117e62cc42431df9d72987b21b0e64f56d8d))
* add a recent commits panel to the dashboard ([#19](https://github.com/lcondliffe/gitling/issues/19)) ([2c127fe](https://github.com/lcondliffe/gitling/commit/2c127fe70d97fe9f5c6239938ccb2c7d04890e17))
* add activity graph drill-down ([f6d64c0](https://github.com/lcondliffe/gitling/commit/f6d64c0558afe5d1c7a8f9c90600b35db9c52359))
* add activity graph drill-down ([4909a3c](https://github.com/lcondliffe/gitling/commit/4909a3cdc96f4d9e62e50bf3033eb538ec6fcee2))
* add branches overview drill-down (AGT-283) ([0c7516b](https://github.com/lcondliffe/gitling/commit/0c7516b2c9b64a37448fcbdcc63fa04a2a3e13db))
* add branches overview drill-down subcommand ([4b4db05](https://github.com/lcondliffe/gitling/commit/4b4db057a6bb287f898c87b12483b215a4c7028b))
* add churn file-churn drill-down (AGT-281) ([39538b0](https://github.com/lcondliffe/gitling/commit/39538b085fd392680a9ef7308ce00dad20a4b7ee))
* add churn file-churn drill-down subcommand ([4d13714](https://github.com/lcondliffe/gitling/commit/4d13714e2e4bed0134e908b82fa5440acb6f9c85))
* add contributors drill-down (AGT-282) ([ceb18bf](https://github.com/lcondliffe/gitling/commit/ceb18bf5be8c2752684f09e68c1b72f8edb53d21))
* add contributors drill-down subcommand ([bda3f18](https://github.com/lcondliffe/gitling/commit/bda3f1829a30d7c28aa120c3d1dea25a9214538d))
* add Homebrew tap release automation ([d52a987](https://github.com/lcondliffe/gitling/commit/d52a9870347ec41d58eb762c38935a483e8f925c))
* add JSON output mode ([0b6096f](https://github.com/lcondliffe/gitling/commit/0b6096fcf65816cb635f13262018aff52fad641e))
* **cache:** add opt-in SQLite backend behind build tag ([#12](https://github.com/lcondliffe/gitling/issues/12)) ([6f5de0e](https://github.com/lcondliffe/gitling/commit/6f5de0e7fd36e4d3b59b584b1200007efad2c2cf))
* **gitdata:** add opt-in go-git backend behind build tag ([#14](https://github.com/lcondliffe/gitling/issues/14)) ([734fb71](https://github.com/lcondliffe/gitling/commit/734fb71b6a30709f03d27f19760511e463c9cacf))
* **render:** lay the dashboard out as boxed panels in a grid ([#20](https://github.com/lcondliffe/gitling/issues/20)) ([6893767](https://github.com/lcondliffe/gitling/commit/6893767c123c8db85bf9c2c9e00232bb7a7224b2))
* **render:** responsive layout to terminal width ([#13](https://github.com/lcondliffe/gitling/issues/13)) ([8e8e6da](https://github.com/lcondliffe/gitling/commit/8e8e6da2336381d47febea4ae3c904f1dbd495ce))
* **tidy:** add a branch cleanup command ([#23](https://github.com/lcondliffe/gitling/issues/23)) ([85f5391](https://github.com/lcondliffe/gitling/commit/85f5391b96f8f36f111bc6244d75c8787dcf9def))
* **vitals:** report live repo state, not just a dirty count ([#22](https://github.com/lcondliffe/gitling/issues/22)) ([81ede5c](https://github.com/lcondliffe/gitling/commit/81ede5c366cd23d258e3ed9b027248d21fb93c58))


### Bug Fixes

* authenticate git pushes and detect new formula files in tap update ([1978890](https://github.com/lcondliffe/gitling/commit/19788906a848bd7b47337819c36e8270e9620218))
* collapse empty graph bucket output ([c43bfa3](https://github.com/lcondliffe/gitling/commit/c43bfa38d1247f03b5e8da27ec417bd8570edd4a))
* harden Homebrew formula PR updates ([47ade17](https://github.com/lcondliffe/gitling/commit/47ade173b409fd6c97e862b4a03e48845f28b5ff))
* keep JSON payload keys consistent ([70654dc](https://github.com/lcondliffe/gitling/commit/70654dc32fcc0a491d92984527f85254f857d40c))
* make release publishing rerunnable ([#18](https://github.com/lcondliffe/gitling/issues/18)) ([2440dfe](https://github.com/lcondliffe/gitling/commit/2440dfe9c6448c64d86531303b83cc9a551e6d35))
* reject conflicting drill-down view requests ([30b7164](https://github.com/lcondliffe/gitling/commit/30b7164566cfafbf7e8472b245f896b842eb7dc2))
* **tidy:** don't offer branches checked out in other worktrees ([#26](https://github.com/lcondliffe/gitling/issues/26)) ([6b08c2e](https://github.com/lcondliffe/gitling/commit/6b08c2e04755d3e83bb2068e81fc06da2ee9fa61))


### Code Refactoring

* remove the optional go-git and sqlite backends ([#24](https://github.com/lcondliffe/gitling/issues/24)) ([958879d](https://github.com/lcondliffe/gitling/commit/958879d3a0794351068bd60af3be8c2cf10abd89))
