package main

import (
	"context"
	"fmt"
	"github.com/achetronic/tapogo/api/types"
	"github.com/achetronic/tapogo/pkg/tapogo"
	"gopkg.in/yaml.v3"
	"log"
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
			return nil, fmt.Errorf("failed to connect to plug: %w", err)
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

func main() {
	// Read YAML config file
	file, err := os.ReadFile("config.yaml")
	if err != nil {
		log.Fatalf("Failed to read YAML file: %v", err)
	}

	// Unmarshal YAML into Config struct
	var config Config
	err = yaml.Unmarshal(file, &config)
	if err != nil {
		log.Fatalf("Failed to parse YAML: %v", err)
	}

	influxClient := influxdb2.NewClient(config.InfluxDB.URL, config.InfluxDB.Token)
	defer influxClient.Close()

	for {
		batterySoC, err := queryInfluxDB(influxClient, config.InfluxDB.Org, `
			from(bucket: "homeassistant")
			  |> range(start: -1h)
			  |> filter(fn: (r) => r["entity_id"] == "battery_state_of_charge")
			  |> filter(fn: (r) => r["_field"] == "value")
			  |> filter(fn: (r) => r["_measurement"] == "%")
			  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
			  |> yield(name: "mean")`)
		if err != nil {
			log.Fatalf("Error querying InfluxDB: %v", err)
		}

		//loadL2, err := queryInfluxDB(influxClient, config.InfluxDB.Org, `
		//	from(bucket: "homeassistant")
		//	  |> range(start: -1h)
		//	  |> filter(fn: (r) => r["entity_id"] == "load_l2")
		//	  |> filter(fn: (r) => r["_field"] == "value")
		//	  |> filter(fn: (r) => r["_measurement"] == "W")
		//	  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
		//	  |> yield(name: "mean")`)
		//if err != nil {
		//	log.Fatalf("Error querying InfluxDB: %v", err)
		//}

		loadL3, err := queryInfluxDB(influxClient, config.InfluxDB.Org, `
			from(bucket: "homeassistant")
			  |> range(start: -1h)
			  |> filter(fn: (r) => r["entity_id"] == "load_l3")
			  |> filter(fn: (r) => r["_field"] == "value")
			  |> filter(fn: (r) => r["_measurement"] == "W")
			  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
			  |> yield(name: "mean")`)
		if err != nil {
			log.Fatalf("Error querying InfluxDB: %v", err)
		}

		pvPower, err := queryInfluxDB(influxClient, config.InfluxDB.Org, `
			from(bucket: "homeassistant")
			  |> range(start: -1h)
			  |> filter(fn: (r) => r["entity_id"] == "pv_power")
			  |> filter(fn: (r) => r["_field"] == "value")
			  |> filter(fn: (r) => r["_measurement"] == "W")
			  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
			  |> yield(name: "mean")`)
		if err != nil {
			log.Fatalf("Error querying InfluxDB: %v", err)
		}

		tuvTemp, err := queryInfluxDB(influxClient, config.InfluxDB.Org, `
			from(bucket: "homeassistant")
			  |> range(start: -1h)
			  |> filter(fn: (r) => r["domain"] == "sensor")
			  |> filter(fn: (r) => r["entity_id"] == "shellyplus1_e465b842dc6c_temperature_2")
			  |> filter(fn: (r) => r["_field"] == "value")
			  |> filter(fn: (r) => r["_measurement"] == "°C")
			  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
			  |> yield(name: "mean")`)
		if err != nil {
			log.Fatalf("Error querying InfluxDB: %v", err)
		}

		var turnOnAction action = "TurnOn"
		var turnOffAction action = "TurnOff"
		var deviceInfoAction action = "DeviceInfo"
		const COOLDOWN = 3

		var tuvPlugCredentials = plugCredentials{
			ip:       config.Tapo.Plugs[1].IP,
			email:    config.Tapo.Email,
			password: config.Tapo.Password,
		}
		tuvPlugInfo, err := controlPlug(deviceInfoAction, tuvPlugCredentials, COOLDOWN)
		if err != nil {
			log.Fatalf("Error getting device info: %v", err)
		}
		tuvPlugState := plugIsOn(tuvPlugInfo)

		if (!tuvPlugState && (batterySoC == 0 || batterySoC == 100) && loadL3 < 1000 && tuvTemp < 67) ||
			(tuvPlugState && (batterySoC == 0 || batterySoC == 100) && loadL3 < 3000 && pvPower > 2000 && tuvTemp < 67) ||
			(!tuvPlugState && batterySoC > 40 && loadL3 < 1000 && pvPower > 2000 && tuvTemp < 67) ||
			(tuvPlugState && batterySoC > 40 && loadL3 < 3000 && pvPower > 2000 && tuvTemp < 67) {
			fmt.Printf("%v TUV: ON - PlugState: %v, SoC: %.1f, L3 Load: %.0f, Temp: %.1f, Panels: %.0f\n", time.Now().Format("2006-01-02 15:04:05"), tuvPlugState, batterySoC, loadL3, tuvTemp, pvPower)
			_, err = controlPlug(turnOnAction, tuvPlugCredentials, COOLDOWN)
			if err != nil {
				log.Fatalf("Error turning on plug: %v", err)
			}
		} else {
			fmt.Printf("%v TUV: OFF - PlugState: %v, SoC: %.1f, L3 Load: %.0f, Temp: %.1f, Panels: %.0f\n", time.Now().Format("2006-01-02 15:04:05"), tuvPlugState, batterySoC, loadL3, tuvTemp, pvPower)
			_, err = controlPlug(turnOffAction, tuvPlugCredentials, COOLDOWN)
			if err != nil {
				log.Fatalf("Error turning off plug: %v", err)
			}
		}

		time.Sleep(150 * time.Second)

		//var heaterPlugCredentials = plugCredentials{
		//	ip:       config.Tapo.Plugs[0].IP,
		//	email:    config.Tapo.Email,
		//	password: config.Tapo.Password,
		//}
		//heaterPlugInfo, err := controlPlug(deviceInfoAction, heaterPlugCredentials, COOLDOWN)
		//if err != nil {
		//	log.Fatalf("Error getting device info: %v", err)
		//}
		//heaterPlugState := plugIsOn(heaterPlugInfo)
		//
		//if (!tuvPlugState && !heaterPlugState && (batterySoC == 0 || batterySoC == 100) && loadL2 < 1000) ||
		//	(!tuvPlugState && heaterPlugState && (batterySoC == 0 || batterySoC == 100) && loadL2 < 3000 && pvPower > 2000) ||
		//	(!tuvPlugState && !heaterPlugState && batterySoC > 60 && loadL2 < 1000 && pvPower > 2000) ||
		//	(!tuvPlugState && heaterPlugState && batterySoC > 60 && loadL2 < 3000 && pvPower > 2000) {
		//	fmt.Printf("%v HEATER: ON - PlugState: %v, SoC: %.1f, L2 Load: %.0f, Panels: %.0f\n", time.Now().Format("2006-01-02 15:04:05"), heaterPlugState, batterySoC, loadL2, pvPower)
		//	_, err = controlPlug(turnOnAction, heaterPlugCredentials, COOLDOWN)
		//	if err != nil {
		//		log.Fatalf("Error turning on plug: %v", err)
		//	}
		//} else {
		//	fmt.Printf("%v HEATER: OFF - PlugState: %v, SoC: %.1f, L2 Load: %.0f, Panels: %.0f\n", time.Now().Format("2006-01-02 15:04:05"), heaterPlugState, batterySoC, loadL2, pvPower)
		//	_, err = controlPlug(turnOffAction, heaterPlugCredentials, COOLDOWN)
		//	if err != nil {
		//		log.Fatalf("Error turning off plug: %v", err)
		//	}
		//}

		time.Sleep(150 * time.Second)
	}
}