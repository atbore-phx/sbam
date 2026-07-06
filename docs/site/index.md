# sbam — Smart Battery Advanced Manager

Charge your Fronius Gen24+ battery intelligently using Solcast weather forecasts.

[Get started](prerequisites.md){ .md-button .md-button--primary }
[View on GitHub](https://github.com/atbore-phx/sbam){ .md-button }

---

## What sbam does

sbam combines three signals your inverter ignores — weather forecasts, daily consumption, and grid rate windows — into a single charging decision. It checks tomorrow's solar production estimate, subtracts what your home will consume, looks at your battery's current charge, and decides whether to charge from the grid during your cheap-rate window.

- **Weather-driven** — uses Solcast solar forecasts, not static schedules
- **MQTT-wired** — publishes state and accepts commands for Home Assistant automations
- **Multi-window** — define multiple charge windows per day, each with its own power limit
- **Open source** — no black box, no vendor lock-in

!!! warning

This project is an unofficial, community-maintained implementation and is not sponsored, endorsed, or supported by Fronius. Use it at your own risk. The software and accompanying documentation are provided "AS IS", without any warranty, express or implied. Neither the author nor any contributor accepts liability for damages, losses, or other consequences resulting from the use of this project or its dependencies.

!!! info

Keep your GEN24 inverter firmware up to date. This integration is tested only on recent firmware versions and may not work correctly on older releases. Firmware versions prior to 1.34.6-1 are known to limit battery charging and are not supported.
