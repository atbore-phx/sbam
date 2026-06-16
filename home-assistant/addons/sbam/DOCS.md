## Home Assistant App Installation and How to Use

This document provides instructions for installing and configuring the SBAM app (add-on) for Home Assistant OS (HAOS).

### Prerequisites
----
SBAM requires the following prerequisites to function correctly:
[SBAM prerequisites](https://github.com/atbore-phx/sbam/blob/main/docs/site/prerequisites.md)

For MQTT features in Home Assistant, enable MQTT support first:
[Home Assistant MQTT integration](https://www.home-assistant.io/integrations/mqtt/)

The recommended broker for Home Assistant users is the Mosquitto add-on.

### Home Assistant Add-on Installation
----
SBAM is available as an App (formerly known as add-ons) for HAOS (Home Assistant OS).

**HAOS must be able to reach the Fronius inverter on its LAN IP.**

Official guide:
[Install a third-party Home Assistant app repository](https://www.home-assistant.io/common-tasks/os#installing-a-third-party-app-repository)

1. Settings
2. Apps

<img width="430" height="332" alt="chrome_ChiVwNM3hN" src="https://github.com/user-attachments/assets/c83e60ba-fbd1-4138-a161-01353ca55eaa" />

3. Install app

<img width="175" height="76" alt="image" src="https://github.com/user-attachments/assets/f9a9a799-465d-45bf-9241-0ce35587fb4f" />

4. Repositories

<img width="489" height="82" alt="image" src="https://github.com/user-attachments/assets/88aace46-bb21-4669-91e9-6a33488519c6" />

5. Add
6. Add repository URL:
https://github.com/atbore-phx/sbam

<img width="449" height="220" alt="image" src="https://github.com/user-attachments/assets/ed59cc24-dd50-4e0d-a84d-708643c0994c" />

Once added:

1. If the add-on is not visible, refresh the page
2. Click the SBAM add-on

![image](https://github.com/user-attachments/assets/ec81f283-fc97-4328-8e1e-ffbd3c4d2e29)

3. Click **Install**

![chrome_NT8Mrf6ls1](https://github.com/atbore-phx/sbam/assets/11421185/cb9eafe3-a274-4164-a789-1c31a87308e1)

4. Enable Start on boot and Watchdog

![chrome_JsiS3CyShs](https://github.com/atbore-phx/sbam/assets/11421185/413e2d3d-638b-417c-b906-34d46aee62c0)

### Configuration
----

Before starting, open the Configuration tab and set options as needed.

For a complete reference of all configuration options — including new v2.1.0 features like multi-window charging, forecast horizons, and the scheduler mode selector — see the [sbam documentation site](https://atbore-phx.github.io/sbam/configuration/).

Save the configuration after editing options.

![SBAM-conf](https://github.com/user-attachments/assets/d0eab452-7b77-4d2c-9b24-7ac44fd50b7a)

### Start SBAM
----

Start SBAM after configuration is complete.

![chrome_5OngSH5IRc](https://github.com/atbore-phx/sbam/assets/11421185/9575b453-5132-4a24-9166-bc6d385690f1)

Check add-on logs for startup and runtime details.

### MQTT Integration
----

When MQTT is enabled, SBAM publishes state and availability topics and subscribes to command topics for Home Assistant integration.
If MQTT broker details are not provided in the configuration, SBAM attempts to auto-fill them from Home Assistant service data.

For full setup, topic mapping, payload schemas, and command examples, see the [MQTT Guide](https://atbore-phx.github.io/sbam/mqtt/).
