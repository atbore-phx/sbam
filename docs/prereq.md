### Allow Battery Charging from the Public Grid

Check if your inverter permits battery charging from the public grid:

1. Open the web interface of the inverter.
2. Select the **"Device Configuration -> Components"** section.
3. Expand the battery section.
4. Enable **"Allow Battery Charging from the Public Grid."**

![allow_charge](https://github.com/user-attachments/assets/3b366999-cf9c-4003-93d4-654d137ba001)

### Enabled Modbus and Solar API:

**Modbus:**

Remote control of the Fronius inverter's charge is only possible by enabling the **"Slave as Modbus TCP"** function: https://www.fronius.com/~/downloads/Solar%20Energy/Operating%20Instructions/42,0410,2049.pdf

To activate this protocol:

1. Open the web interface of the inverter
2. Select the **"Communication"** section
3. Open the **"Modbus"** menu
4. Activate **Slave (Secondary inverter) Modbus TCP**
5. Tcp port: 502
6. Model Type: int +SF

![image](https://github.com/user-attachments/assets/afadb0b5-1edb-461c-919a-9fd249029f94)

**Solar API:**

Sbam uses the local Fronius API to retrieve data related to the battery:

1. Open the web interface of the inverter
2. Select the **"Communication"** section
3. Open the **"Solar API"** menu
4. Enable **Solar API Communication**

![chrome_uZTCoI1O2f](https://github.com/atbore-phx/sbam/assets/11421185/818eddd1-678f-45ba-8081-9958882786cf)

### Subscription to the Solcast forecasting service:

I chose the site solcast.com for weather forecasts and solar production estimates. I have tried many but I consider Solcast the best in the Freemium category (Max 10 API calls/day): https://solcast.com/free-rooftop-solar-forecasting

After adding your installation, you will obtain a forecast link like this:

```
https://api.solcast.com.au/rooftop_sites/your-site/forecasts?format=json
```

Where _your-site_ is an identifier of your installation. Copy it as it will be needed later for the configuration of sbam.

The last step is to obtain the API key from the Solcast site:

- Click on your name at the top right
- **Your Api Key**
- Generate the ApiKey and copy it as it will be needed later for the configuration of sbam.
