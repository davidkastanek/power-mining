# Power Mining

This script aims to utilize surplus photovoltaic energy for heating utility water. It reads various metrics from InfluxDB, and when the specified conditions are met, it turns on a Tapo smart socket to activate the electric water heater.

## Usage
- Configure the conditions in the `config.yaml` file.
- Run the script using `go run .`
