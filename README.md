[![codecov](https://codecov.io/gh/atbore-phx/sbam/graph/badge.svg?token=0fgSvHFiTx)](https://codecov.io/gh/atbore-phx/sbam)

# sbam - Smart Battery Advanced Manager.

Charge Fronius battery using SolCast weather forecast.

> [!CAUTION]
> This project is an unofficial, community-maintained implementation and is not sponsored, endorsed, or supported by Fronius. Use it at your own risk. The software and accompanying documentation are provided "AS IS", without any warranty, express or implied. Neither the author nor any contributor accepts liability for damages, losses, or other consequences resulting from the use of this project or its dependencies.

> [!IMPORTANT]
> Keep your GEN24 inverter firmware up to date. This integration is tested only on recent firmware versions and may not work correctly on older releases. We strongly recommend updating your inverter to the latest official firmware before using sbam. Always review Fronius release notes and back up your inverter settings before updating. Firmware versions prior to 1.34.6-1 are known to limit battery charging and are not supported.

## Introduction

After installing a Fronius Gen24 plus Solar system including a BYD battery, I wanted during months of low solar production to charge the battery at night when electricity costs are generally lower, in order to use the stored energy during the day.

Fronius through the local web interface reachable from the inverter's LAN IP provides the **Battery Management** utility -> **Time-dependent battery control**.

Indeed, it is possible to charge at night, but the process is static. Many times, I found the battery to be either too charged or too discharged the next day. I wanted something more advanced, dynamic, and adaptive that takes into account:

- weather forecasts
- daily electricity consumption related to my home
- the current battery charge
- the minimum reserve of the battery not to go below
- the time range when the energy operator offers cheaper electricity to force the charge.

NOTE:
In Solar.web, the energy balance does not display grid charge information to prevent customers from perceiving a higher consumption than actual. This is done to simplify the Solar.web view. (Source: Official Fronius support)

Here **sbam** is all this and much more :)

## Documentation

Full documentation is available at **[atbore-phx.github.io/sbam](https://atbore-phx.github.io/sbam/)**:

| Section | |
|---------|--|
| [Prerequisites](https://atbore-phx.github.io/sbam/prerequisites/) | Inverter setup, Solcast API |
| [Installation](https://atbore-phx.github.io/sbam/installation/) | Home Assistant add-on, Docker, standalone |
| [Configuration](https://atbore-phx.github.io/sbam/configuration/) | Every config option with deployment-specific notes |
| [MQTT Guide](https://atbore-phx.github.io/sbam/mqtt/) | Topic map, payload schemas, commands |
| [CLI Reference](https://atbore-phx.github.io/sbam/cli/) | Full command-line reference |
| [Changelog](https://atbore-phx.github.io/sbam/changelog/) | Release history |

### Home Assistant Add-on

sbam is available as an App (add-on) for Home Assistant OS. [Installation guide](https://atbore-phx.github.io/sbam/installation/)

### Contributions
----
sbam is an open-source, community-driven project so contributions are welcome
from everyone. You can file issues to report bugs or request features,
improve documentation, add tests, or submit pull requests with proposed
changes. For larger features please open an issue first to discuss scope;
small fixes and documentation updates can be submitted directly as PRs.
When submitting code, include tests where applicable and keep changes focused so reviews are easy.

#### Support :heart:

If you don't code but want to support the project, you can sponsor the project on GitHub Sponsors:

[![Sponsor on GitHub](https://img.shields.io/badge/Sponsor-GitHub-6f42c1?style=for-the-badge&logo=github)](https://github.com/sponsors/atbore-phx)

#### Compound Engineering

This project uses [Compound Engineering](https://github.com/EveryInc/compound-engineering-plugin) skills for feature development — brainstorm, plan, implement, review, and ship with a consistent, quality-gated workflow. Contributor guidance and examples live in [docs/vibe](docs/vibe). Always validate generated code, run tests, and require human review before merging.
