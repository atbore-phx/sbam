# Installation

## Home Assistant Add-on

sbam is available as an App (add-on) for Home Assistant OS (HAOS).

**HAOS must be able to reach the Fronius inverter on its LAN IP.**

### Add Repository

1. In Home Assistant, go to **Settings → Add-ons → Add-on Store**
2. Click the three-dot menu → **Repositories**
3. Add the repository URL: `https://github.com/atbore-phx/sbam`

![Add repository](https://github.com/user-attachments/assets/ed59cc24-dd50-4e0d-a84d-708643c0994c)

### Install

1. If the add-on is not visible, refresh the page
2. Click the **sbam** add-on

![SBAM add-on](https://github.com/user-attachments/assets/ec81f283-fc97-4328-8e1e-ffbd3c4d2e29)

3. Click **Install**

![Install](https://github.com/atbore-phx/sbam/assets/11421185/cb9eafe3-a274-4164-a789-1c31a87308e1)

### Configure

Before starting, open the **Configuration** tab and set options as needed. See the [Configuration](configuration.md) page for a complete reference of all options.

![Configuration](https://github.com/user-attachments/assets/d0eab452-7b77-4d2c-9b24-7ac44fd50b7a)

### Start

1. Enable **Start on boot** and **Watchdog**

![Watchdog](https://github.com/atbore-phx/sbam/assets/11421185/413e2d3d-638b-417c-b906-34d46aee62c0)

2. Click **Start**

![Start](https://github.com/atbore-phx/sbam/assets/11421185/9575b453-5132-4a24-9166-bc6d385690f1)

3. Check add-on logs for startup and runtime details.

## Docker

```bash
docker run \
  -e URL="https://api.solcast.com.au/rooftop_sites/your-site/forecasts?format=json" \
  -e APIKEY="your-api-key" \
  -e FRONIUS_IP="192.168.1.100" \
  -e PW_CONSUMPTION="11000" \
  ghcr.io/atbore-phx/ha-amd64-sbam:latest \
  sbam schedule
```

Mount a `config.yaml` for persistent configuration:

```bash
docker run -v $(pwd)/config.yaml:/config.yaml \
  ghcr.io/atbore-phx/ha-amd64-sbam:latest
```

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
