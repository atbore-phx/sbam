## Installation and How to Use:

### Prerequisites

sbam requires the following prerequisites to function correctly: [link](../../../docs/prereq.md)

### Home Assistant:

Sbam is available as an add-on for HAOS (Home Assistant OS).
**N.B. HAOS must be able to reach the Fronius inverter on its LAN IP.**

**Add the git repository**

official guide: https://www.home-assistant.io/common-tasks/os#installing-third-party-add-ons

1. Settings
2. Add-ons

![chrome_icgQkIQh6J](https://github.com/atbore-phx/sbam/assets/11421185/531eeab3-9910-4fb8-bf71-22d09ec77f95)

3. ADD-ON STORE

![chrome_hEKXVTu6tY](https://github.com/atbore-phx/sbam/assets/11421185/eec5866d-4a5c-4ae0-bd57-05a10fc48b67)

4. Repositories

![chrome_thaaqxEFgT](https://github.com/atbore-phx/sbam/assets/11421185/38bbcb7d-b3c7-4cbc-ba13-4d55292786ef)

5. Add -> https://github.com/atbore-phx/sbam

![chrome_oAyxTDCxUK](https://github.com/atbore-phx/sbam/assets/11421185/bdefb7c5-04d1-4d20-892a-bc864907da31)

Once added, it can be installed:

1. If the add-on is not visible, refresh the page with F5
2. Click the sbam add-on

![image](https://github.com/user-attachments/assets/ec81f283-fc97-4328-8e1e-ffbd3c4d2e29)

3. **Install**

![chrome_NT8Mrf6ls1](https://github.com/atbore-phx/sbam/assets/11421185/cb9eafe3-a274-4164-a789-1c31a87308e1)

4. Enable **Start on boot** and **Watchdog**

![chrome_JsiS3CyShs](https://github.com/atbore-phx/sbam/assets/11421185/413e2d3d-638b-417c-b906-34d46aee62c0)

Do not start yet but configure it:

1. Click on the configuration tab
2. **url:** Solcast forecast site address (replace <YOUR-SITE> with your identifier). Multiple addresses are supported (max. 2); separate them with a comma (,) If **2 url**: API calls is split evenly, 5 API calls per array per **UTC** day.
3. **apikey:** Solcast API key
4. **fronius_ip:** Fronius inverter LAN IP
5. **start_hr:** Start time of the advantageous network operator rate (default 00:00)
6. **end_hr:** End time of the advantageous network operator rate (default 06:00)
7. **crontab:** Crontab to run sbam (default: 00 00-05 \* \* \*) **2 url:** **CET**:  10 00,03,05,06 \* \* \*  (10 min past 00,03,05,06)  **UTC** you may add 1 additional hour/API call. Ie:  10 00,03,05,06,07 \* \* \* *Frequent calls close to end time to get most accurate forecast and allow time to charge.* 
8. **pw_consumption:** Daily electrical consumption in Wh (Default: 11000, means 11kWh)
9. **max_charge:** Maximum amount of power required from the electricity network to charge the battery in W (Default: 3500)
10. **pw_batt_reserve:** Minimum battery capacity to maintain in Wh (Default: 4000, means 4kWh)
11. **defaults:** At the end of the crontab cycle, reconfigure the Fronius inverter to default (automatic management).

![chrome_FibpWCPrIW](https://github.com/atbore-phx/sbam/assets/11421185/7d17c36b-9e7c-4499-a0f9-557d0ddbe7bb)

Finally Start **sbam**!

![chrome_5OngSH5IRc](https://github.com/atbore-phx/sbam/assets/11421185/9575b453-5132-4a24-9166-bc6d385690f1)

Check the logs for any other further info.
