# Power Mining

This script aims to utilize surplus photovoltaic energy for heating utility water. It reads various metrics from InfluxDB, and when the specified conditions are met, it turns on a Tapo smart socket to activate the electric water heater.

## Usage
### Configuration
Create file called `config.yaml` in the same directory as the executable
```yaml
influxdb:
  url: http://<ip>:<port>
  token: <influxdb-token>
  org: <influxdb-org>
  bucket: <influxdb-bucket>
tapo:
  email: <tapo-username>
  password: <tapo-password>
  plugs:
    - name: heater
      ip: <heater-tapo-plug-ip>
    - name: tuv
      ip: <tuv-tapo-plug-ip>
thresholds:
  maxTemp: 67 # max temperature to which water is heated
  maxLoad: 3300 # max power per electrical phase
  minTuvSoC: 40 # min SoC percentage for water to start being heated
  minPanels: 2000 # min power available from solar panels
  maxSoC: 100 # max percentage value for SoC
  maxSoCNil: 0 # max value for SoC when metric not available
  loadIdle: 1000 # max active power on electrical phase for that phase to be considered available for more load
  minHeaterSoC: 60 # min SoC percentage for power being considered to be sent to the air heater
  panels1Idle: 510 # min voltage on string 1 for panels to be idle when sun is shining
  panels1Min: 300 # min voltage on string 1 for panels when sun is shining
healthcheckPort: <healthcheck-port>
```
Feel free to adjust the values for thresholds for your setup.
### Build
Run `go build .` to generate the executable (`power-mining` for mac/unix, `power-mining.exe` for windows)
### Run
- `go run .`
- `./power-mining`
- `./power-mining -log-to-file=power-mining.log` for logging into file

## Health Check
There is an endpoint `:<healthcheck-port>/health` to check whether the script is running or not
