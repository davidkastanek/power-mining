package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/achetronic/tapogo/api/types"
	"github.com/achetronic/tapogo/pkg/tapogo"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"io"
	"net/http"
	"os"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

type Config struct {
	InfluxDB struct {
		URL    string `yaml:"url"`
		Token  string `yaml:"token"`
		Org    string `yaml:"org"`
		Bucket string `yaml:"bucket"`
	} `yaml:"influxdb"`
	Tapo struct {
		Email    string `yaml:"email"`
		Password string `yaml:"password"`
		Plugs    []struct {
			Name string `yaml:"name"`
			IP   string `yaml:"ip"`
		} `yaml:"plugs"`
	} `yaml:"tapo"`
	Thresholds struct {
		MaxTemp      float64 `yaml:"maxTemp"`
		MaxLoad      float64 `yaml:"maxLoad"`
		MinTuvSoC    float64 `yaml:"minTuvSoC"`
		MinPanels    float64 `yaml:"minPanels"`
		MaxSoC       float64 `yaml:"maxSoC"`
		MaxSoCNil    float64 `yaml:"maxSoCNil"`
		LoadIdle     float64 `yaml:"loadIdle"`
		MinHeaterSoC float64 `yaml:"minHeaterSoC"`
		Panels1Idle  float64 `yaml:"panels1Idle"`
		Panels1Min   float64 `yaml:"panels1Min"`
	}
	HealthCheckPort int `yaml:"healthCheckPort"`
}

type plugCredentials struct {
	ip       string
	email    string
	password string
}

type action string

func controlPlug(action action, credentials plugCredentials, cooldown time.Duration) (*types.ResponseSpec, error) {
	for {
		plug, err := tapogo.NewTapo(credentials.ip, credentials.email, credentials.password, &tapogo.TapoOptions{})
		if err != nil {
			time.Sleep(cooldown * time.Second)
			continue
			//return nil, fmt.Errorf("failed to connect to plug: %w", err)
		}

		switch action {
		case "TurnOn":
			response, err := plug.TurnOn()
			if err != nil {
				time.Sleep(cooldown * time.Second)
			} else {
				return response, nil
			}
		case "TurnOff":
			response, err := plug.TurnOff()
			if err != nil {
				time.Sleep(cooldown * time.Second)
			} else {
				return response, nil
			}
		case "DeviceInfo":
			response, err := plug.DeviceInfo()
			if err != nil {
				time.Sleep(cooldown * time.Second)
			} else {
				return response, nil
			}
		default:
			return nil, fmt.Errorf("unknown action: %s", action)
		}
	}
}

func plugIsOn(response *types.ResponseSpec) bool {
	return response.Result.DeviceOn
}

func queryInfluxDB(client influxdb2.Client, org string, fluxQuery string) (float64, error) {
	queryAPI := client.QueryAPI(org)
	result, err := queryAPI.Query(context.Background(), fluxQuery)
	if err != nil {
		return 0, fmt.Errorf("error querying influxdb: %v", err)
	}

	var value float64
	for result.Next() {
		value = result.Record().Value().(float64)
	}
	if result.Err() != nil {
		return 0, fmt.Errorf("error parsing influxdb query result: %v", result.Err())
	}
	return value, nil
}

func getBatterySoC(client influxdb2.Client, org string) (float64, error) {
	return queryInfluxDB(client, org, `
			from(bucket: "homeassistant")
			  |> range(start: -1h)
			  |> filter(fn: (r) => r["entity_id"] == "battery_state_of_charge")
			  |> filter(fn: (r) => r["_field"] == "value")
			  |> filter(fn: (r) => r["_measurement"] == "%")
			  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
			  |> yield(name: "mean")`)
}

func getLoad(client influxdb2.Client, org string, line string) (float64, error) {
	query := fmt.Sprintf(`
		from(bucket: "homeassistant")
		|> range(start: -1h)
		|> filter(fn: (r) => r["entity_id"] == "load_%s")
		|> filter(fn: (r) => r["_field"] == "value")
		|> filter(fn: (r) => r["_measurement"] == "W")
		|> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
		|> yield(name: "mean")`, line)
	return queryInfluxDB(client, org, query)
}

func getStringVoltage(client influxdb2.Client, org string, stringId string) (float64, error) {
	query := fmt.Sprintf(`
		from(bucket: "homeassistant")
		|> range(start: -1h)
		|> filter(fn: (r) => r["entity_id"] == "pv%s_voltage")
		|> filter(fn: (r) => r["_field"] == "value")
		|> filter(fn: (r) => r["_measurement"] == "V")
		|> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
		|> yield(name: "mean")`, stringId)
	return queryInfluxDB(client, org, query)
}

func getPvPower(client influxdb2.Client, org string) (float64, error) {
	return queryInfluxDB(client, org, `
			from(bucket: "homeassistant")
			  |> range(start: -1h)
			  |> filter(fn: (r) => r["entity_id"] == "pv_power")
			  |> filter(fn: (r) => r["_field"] == "value")
			  |> filter(fn: (r) => r["_measurement"] == "W")
			  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
			  |> yield(name: "mean")`)
}

func getTuvTemp(client influxdb2.Client, org string) (float64, error) {
	return queryInfluxDB(client, org, `
			from(bucket: "homeassistant")
			  |> range(start: -1h)
			  |> filter(fn: (r) => r["domain"] == "sensor")
			  |> filter(fn: (r) => r["entity_id"] == "shellyplus1_e465b842dc6c_temperature_2")
			  |> filter(fn: (r) => r["_field"] == "value")
			  |> filter(fn: (r) => r["_measurement"] == "°C")
			  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
			  |> yield(name: "mean")`)
}

func getConfigFromYaml(filename string) (*Config, error) {
	// Read the YAML file
	file, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}

	// Unmarshal the YAML into a Config struct
	var config Config
	err = yaml.Unmarshal(file, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %v", err)
	}

	return &config, nil
}

func startHealthCheck(port int) {
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := fmt.Fprintln(w, "OK")
		if err != nil {
			log.Fatalf("Error writing response body: %v", err)
		}
	})

	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil); err != nil {
		log.Fatalf("Error starting health check server on port %s: %v", port, err)
	}
}

func main() {
	logToFile := flag.String("log-to-file", "", "Log output to file at provided location")
	flag.Parse()
	if *logToFile != "" {
		file, err := os.OpenFile(*logToFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatalf("error opening log file: %v", err)
		}
		log.SetOutput(io.MultiWriter(os.Stdout, file))
	}

	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:          true,
		TimestampFormat:        "2006-01-02 15:04:05",
		DisableLevelTruncation: true,
		DisableColors:          true,
		DisableSorting:         true,
		DisableQuote:           true,
	})
	log.SetLevel(log.DebugLevel)
	const (
		Green = "\033[32m"
		Red   = "\033[31m"
		White = "\033[97m"
		Reset = "\033[0m"
	)

	config, err := getConfigFromYaml("config.yaml")
	if err != nil {
		log.Fatalf("Failed to parse YAML: %v", err)
	}

	go startHealthCheck(config.HealthCheckPort)

	influxClient := influxdb2.NewClient(config.InfluxDB.URL, config.InfluxDB.Token)
	defer influxClient.Close()

	for {
		batterySoC, err := getBatterySoC(influxClient, config.InfluxDB.Org)
		if err != nil {
			log.Fatalf("Error getting batterySoC: %v", err)
		}

		loadL2, err := getLoad(influxClient, config.InfluxDB.Org, "l2")
		if err != nil {
			log.Fatalf("Error getting loadL2: %v", err)
		}

		loadL3, err := getLoad(influxClient, config.InfluxDB.Org, "l3")
		if err != nil {
			log.Fatalf("Error getting loadL3: %v", err)
		}

		pvPower, err := getPvPower(influxClient, config.InfluxDB.Org)
		if err != nil {
			log.Fatalf("Error getting pvPower: %v", err)
		}

		tuvTemp, err := getTuvTemp(influxClient, config.InfluxDB.Org)
		if err != nil {
			log.Fatalf("Error getting tuvTemp: %v", err)
		}

		string1Voltage, err := getStringVoltage(influxClient, config.InfluxDB.Org, "1")
		if err != nil {
			log.Fatalf("Error getting string1Voltage: %v", err)
		}

		var turnOnAction action = "TurnOn"
		var turnOffAction action = "TurnOff"
		var deviceInfoAction action = "DeviceInfo"
		const cooldown = 3

		var tuvPlugCredentials = plugCredentials{
			ip:       config.Tapo.Plugs[1].IP,
			email:    config.Tapo.Email,
			password: config.Tapo.Password,
		}
		tuvPlugInfo, err := controlPlug(deviceInfoAction, tuvPlugCredentials, cooldown)
		if err != nil {
			log.Fatalf("Error getting device info: %v", err)
		}

		isTuvPlugOn := plugIsOn(tuvPlugInfo)
		isBatteryAtMax := batterySoC == config.Thresholds.MaxSoCNil || batterySoC == config.Thresholds.MaxSoC
		isTuvCold := tuvTemp < config.Thresholds.MaxTemp
		isL3LoadLow := loadL3 < config.Thresholds.LoadIdle
		isL3LoadWithinLimit := loadL3 < config.Thresholds.MaxLoad
		isSunShining := string1Voltage > config.Thresholds.Panels1Idle
		isSunShiningEnough := pvPower > config.Thresholds.MinPanels
		isDayTime := string1Voltage > config.Thresholds.Panels1Min

		shouldTuvTurnOn := (!isTuvPlugOn && isBatteryAtMax && isL3LoadLow && isTuvCold && isSunShining) ||
			(isTuvPlugOn && isBatteryAtMax && isL3LoadWithinLimit && isSunShiningEnough && isTuvCold && isSunShining) ||
			(!isTuvPlugOn && batterySoC > config.Thresholds.MinTuvSoC && isL3LoadLow && isSunShiningEnough && isTuvCold && isDayTime) ||
			(isTuvPlugOn && batterySoC > config.Thresholds.MinTuvSoC && isL3LoadWithinLimit && isSunShiningEnough && isTuvCold)

		fields := log.Fields{
			"batterySoC":          fmt.Sprintf("%.1f", batterySoC),
			"loadL3":              fmt.Sprintf("%.0f", loadL3),
			"tuvTemp":             fmt.Sprintf("%.1f", tuvTemp),
			"Panels":              fmt.Sprintf("%.0f", pvPower),
			"string1Voltage":      fmt.Sprintf("%.0f", string1Voltage),
			"isTuvPlugOn":         isTuvPlugOn,
			"isBatteryAtMax":      isBatteryAtMax,
			"isTuvCold":           isTuvCold,
			"isL3LoadLow":         isL3LoadLow,
			"isL3LoadWithinLimit": isL3LoadWithinLimit,
			"isSunShining":        isSunShining,
			"isDayTime":           isDayTime,
		}

		if shouldTuvTurnOn {
			log.WithFields(fields).Debugf("%sTUV: %sON%s", White, Green, Reset)
			_, err = controlPlug(turnOnAction, tuvPlugCredentials, cooldown)
			isTuvPlugOn = true
			if err != nil {
				log.Fatalf("Error turning on plug: %v", err)
			}
		} else {
			log.WithFields(fields).Debugf("%sTUV: %sOFF%s", White, Red, Reset)
			_, err = controlPlug(turnOffAction, tuvPlugCredentials, cooldown)
			isTuvPlugOn = false
			if err != nil {
				log.Fatalf("Error turning off plug: %v", err)
			}
		}

		time.Sleep(60 * time.Second)

		var heaterPlugCredentials = plugCredentials{
			ip:       config.Tapo.Plugs[0].IP,
			email:    config.Tapo.Email,
			password: config.Tapo.Password,
		}
		heaterPlugInfo, err := controlPlug(deviceInfoAction, heaterPlugCredentials, cooldown)
		if err != nil {
			log.Fatalf("Error getting device info: %v", err)
		}
		isHeaterPlugOn := plugIsOn(heaterPlugInfo)
		isL2LoadLow := loadL2 < config.Thresholds.LoadIdle
		isL2LoadWithinLimit := loadL2 < config.Thresholds.MaxLoad

		shouldHeaterTurnOn := (!isTuvPlugOn && !isHeaterPlugOn && isBatteryAtMax && isL2LoadLow && isSunShining) ||
			(!isTuvPlugOn && isHeaterPlugOn && isBatteryAtMax && isL2LoadWithinLimit && isSunShiningEnough && isSunShining) ||
			(!isTuvPlugOn && !isTuvCold && !isHeaterPlugOn && batterySoC > config.Thresholds.MinHeaterSoC && isL2LoadLow && isSunShiningEnough && isDayTime) ||
			(!isTuvPlugOn && !isTuvCold && isHeaterPlugOn && batterySoC > config.Thresholds.MinHeaterSoC && isL2LoadWithinLimit && isSunShiningEnough)

		fields = log.Fields{
			"batterySoC":          fmt.Sprintf("%.1f", batterySoC),
			"loadL2":              fmt.Sprintf("%.0f", loadL2),
			"Panels":              fmt.Sprintf("%.0f", pvPower),
			"string1Voltage":      fmt.Sprintf("%.0f", string1Voltage),
			"isTuvPlugOn":         isTuvPlugOn,
			"isHeaterPlugOn":      isHeaterPlugOn,
			"isBatteryAtMax":      isBatteryAtMax,
			"isL2LoadLow":         isL2LoadLow,
			"isL2LoadWithinLimit": isL2LoadWithinLimit,
			"isSunShining":        isSunShining,
			"isDayTime":           isDayTime,
		}

		if shouldHeaterTurnOn {
			log.WithFields(fields).Debugf("%sHEATER: %sON%s", White, Green, Reset)
			_, err = controlPlug(turnOnAction, heaterPlugCredentials, cooldown)
			isHeaterPlugOn = true
			if err != nil {
				log.Fatalf("Error turning on plug: %v", err)
			}
		} else {
			log.WithFields(fields).Debugf("%sHEATER: %sOFF%s", White, Red, Reset)
			_, err = controlPlug(turnOffAction, heaterPlugCredentials, cooldown)
			isHeaterPlugOn = false
			if err != nil {
				log.Fatalf("Error turning off plug: %v", err)
			}
		}

		time.Sleep(60 * time.Second)
	}
}
