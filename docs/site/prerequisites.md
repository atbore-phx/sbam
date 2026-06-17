# Prerequisites

sbam requires these prerequisites to function correctly.

## Allow Battery Charging from the Public Grid

Check if your inverter permits battery charging from the public grid:

1. Open the web interface of the inverter
2. Select **Device Configuration → Components**
3. Expand the battery section
4. Enable **"Allow Battery Charging from the Public Grid"**

![Allow charge setting](https://github.com/user-attachments/assets/3b366999-cf9c-4003-93d4-654d137ba001)

## Enable Modbus and Solar API

### Modbus

Remote control of the Fronius inverter's charge requires enabling the **"Slave as Modbus TCP"** function.

1. Open the web interface of the inverter
2. Select the **Communication** section
3. Open the **Modbus** menu
4. Activate **Slave (Secondary inverter) Modbus TCP**
5. TCP port: 502
6. Model Type: int +SF

![Modbus settings](https://github.com/user-attachments/assets/afadb0b5-1edb-461c-919a-9fd249029f94)

### Solar API

sbam uses the local Fronius API to retrieve battery data:

1. Open the web interface of the inverter
2. Select the **Communication** section
3. Open the **Solar API** menu
4. Enable **Solar API Communication**

![Solar API setting](https://github.com/atbore-phx/sbam/assets/11421185/818eddd1-678f-45ba-8081-9958882786cf)

## Solcast Forecasting Service

sbam uses [Solcast](https://solcast.com/free-rooftop-solar-forecasting) for weather forecasts and solar production estimates. The free tier allows up to 10 API calls per day.

After adding your installation, you will obtain a forecast link:

```
https://api.solcast.com.au/rooftop_sites/your-site/forecasts?format=json
```

Copy it — you will need it for the `url` configuration option.

### API Key

1. Click on your name at the top right on the Solcast site
2. Select **Your API Key**
3. Generate the API key and copy it — you will need it for the `apikey` configuration option
