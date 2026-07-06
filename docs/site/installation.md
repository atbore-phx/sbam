# Installation

## Home Assistant Add-on

sbam is available as an App (add-on) for Home Assistant OS (HAOS).

**HAOS must be able to reach the Fronius inverter on its LAN IP.**

Official Home assistant guide: [Install a third-party Home Assistant app repository](https://www.home-assistant.io/common-tasks/os#installing-a-third-party-app-repository)

### Add Repository

1. In Home Assistant, go to **Settings → Apps → Install app**
2. Click the three-dot menu → **Repositories**
3. Click **Add**
4. Add the repository URL: `https://github.com/atbore-phx/sbam`
5. Click **Add** to confirm

<img width="817" height="345" alt="Screenshot 2026-06-17 142128" src="https://github.com/user-attachments/assets/8438e48a-5451-47e9-9833-9b119cd5bb99" />


### Install

1. If the app is not visible, refresh the page
2. Click the **sbam** app
3. Click **Install**

![SBAM app](https://github.com/user-attachments/assets/ec81f283-fc97-4328-8e1e-ffbd3c4d2e29)

![Install](https://github.com/atbore-phx/sbam/assets/11421185/cb9eafe3-a274-4164-a789-1c31a87308e1)

### Configure

Before starting, open the **Configuration** tab and set options as needed. See the [Configuration](configuration.md) page for a complete reference of all options.

<img width="1362" height="495" alt="Screenshot 2026-06-17 154313" src="https://github.com/user-attachments/assets/4dc73d9c-35fd-4aad-99b9-e1b1b267a914" />

### Start

1. Enable **Start on boot** and **Watchdog**
2. Click **Start**
3. Check add-on logs for startup and runtime details.

![Watchdog](https://github.com/atbore-phx/sbam/assets/11421185/413e2d3d-638b-417c-b906-34d46aee62c0)

![Start](https://github.com/atbore-phx/sbam/assets/11421185/9575b453-5132-4a24-9166-bc6d385690f1)

## Standalone Binary

Download the latest binary from [GitHub Releases](https://github.com/atbore-phx/sbam/releases/latest) and run:

```bash
./sbam schedule --apikey "your-key" --url "https://api.solcast.com.au/..." --fronius_ip "192.168.1.100"
```

Configuration values are resolved with this precedence (highest first):

1. CLI flag (e.g., `--url http://example/`)
2. Environment variable (e.g., `URL=http://example/`)
3. `config.yaml` in the working directory
4. Built-in default

For a full list of commands and flags, see the [CLI Reference](cli.md).
