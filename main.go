package main

import (
	"context"
	"fmt"
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
		Email string `yaml:"email"`
		Pass  string `yaml:"pass"`
		Plugs []struct {
			Name string `yaml:"name"`
			IP   string `yaml:"ip"`
		} `yaml:"plugs"`
	} `yaml:"tapo"`
}

func plugTurnOn(plug *tapogo.Tapo, ip string, email string, pass string, cooldown time.Duration) (*tapogo.Tapo, error) {
	_, err := plug.TurnOn()
	if err != nil {
		for {
			//fmt.Printf("Failed to turn on plug, retrying in %v ...\n", cooldown*time.Second)
			time.Sleep(cooldown * time.Second)
			newP, err := newPlug(ip, email, pass)
			if err != nil {
				return nil, err
			}
			_, err = newP.TurnOn()
			if err == nil {
				//fmt.Println("Plug turned on")
				return newP, nil
			}
		}
	}
	return plug, nil
}

func plugIsOn(plug *tapogo.Tapo, ip string, email string, pass string, cooldown time.Duration) (bool, *tapogo.Tapo, error) {
	info, err := plug.DeviceInfo()
	if err != nil {
		for {
			//fmt.Printf("Failed to get plug state, retrying in %v ...\n", cooldown*time.Second)
			time.Sleep(cooldown * time.Second)
			newP, err := newPlug(ip, email, pass)
			if err != nil {
				return false, nil, err
			}
			info, err = newP.DeviceInfo()
			if err == nil {
				//fmt.Println("Plug state gathered")
				return info.Result.DeviceOn, newP, nil
			}
		}
	}
	return info.Result.DeviceOn, plug, nil
}

func plugTurnOff(plug *tapogo.Tapo, ip string, email string, pass string, cooldown time.Duration) (*tapogo.Tapo, error) {
	_, err := plug.TurnOff()
	if err != nil {
		for {
			//fmt.Printf("Failed to turn off plug, retrying in %v ...\n", cooldown*time.Second)
			time.Sleep(cooldown * time.Second)
			newP, err := newPlug(ip, email, pass)
			if err != nil {
				return nil, err
			}
			_, err = newP.TurnOff()
			if err == nil {
				//fmt.Println("Plug turned off")
				return newP, nil
			}
		}
	}
	return plug, nil
}

func newPlug(ip string, email string, password string) (*tapogo.Tapo, error) {
	plug, err := tapogo.NewTapo(ip, email, password, &tapogo.TapoOptions{})
	if err != nil {
		return nil, fmt.Errorf("error connecting to plug: %v", err)
	}

	return plug, nil
}

func queryInfluxDB(client influxdb2.Client, org string, fluxQuery string) float64 {
	queryAPI := client.QueryAPI(org)
	result, err := queryAPI.Query(context.Background(), fluxQuery)
	if err != nil {
		log.Fatalf("Query failed: %v\n", err)
	}

	var value float64
	for result.Next() {
		value = result.Record().Value().(float64)
	}
	if result.Err() != nil {
		log.Fatalf("Query parsing failed: %v\n", result.Err().Error())
	}
	return value
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
		batterySoC := queryInfluxDB(influxClient, config.InfluxDB.Org, `
			from(bucket: "homeassistant")
			  |> range(start: -1h)
			  |> filter(fn: (r) => r["entity_id"] == "battery_state_of_charge")
			  |> filter(fn: (r) => r["_field"] == "value")
			  |> filter(fn: (r) => r["_measurement"] == "%")
			  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
			  |> yield(name: "mean")`)

		//loadL2 := queryInfluxDB(influxClient, config.InfluxDB.Org, `
		//	from(bucket: "homeassistant")
		//	  |> range(start: -1h)
		//	  |> filter(fn: (r) => r["entity_id"] == "load_l2")
		//	  |> filter(fn: (r) => r["_field"] == "value")
		//	  |> filter(fn: (r) => r["_measurement"] == "W")
		//	  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
		//	  |> yield(name: "mean")`)

		loadL3 := queryInfluxDB(influxClient, config.InfluxDB.Org, `
			from(bucket: "homeassistant")
			  |> range(start: -1h)
			  |> filter(fn: (r) => r["entity_id"] == "load_l3")
			  |> filter(fn: (r) => r["_field"] == "value")
			  |> filter(fn: (r) => r["_measurement"] == "W")
			  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
			  |> yield(name: "mean")`)

		pvPower := queryInfluxDB(influxClient, config.InfluxDB.Org, `
			from(bucket: "homeassistant")
			  |> range(start: -1h)
			  |> filter(fn: (r) => r["entity_id"] == "pv_power")
			  |> filter(fn: (r) => r["_field"] == "value")
			  |> filter(fn: (r) => r["_measurement"] == "W")
			  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
			  |> yield(name: "mean")`)

		tuvTemp := queryInfluxDB(influxClient, config.InfluxDB.Org, `
			from(bucket: "homeassistant")
			  |> range(start: -1h)
			  |> filter(fn: (r) => r["domain"] == "sensor")
			  |> filter(fn: (r) => r["entity_id"] == "shellyplus1_e465b842dc6c_temperature_2")
			  |> filter(fn: (r) => r["_field"] == "value")
			  |> filter(fn: (r) => r["_measurement"] == "°C")
			  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
			  |> yield(name: "mean")`)

		var tuvPlug *tapogo.Tapo
		tuvPlug, err = tapogo.NewTapo(config.Tapo.Plugs[1].IP, config.Tapo.Email, config.Tapo.Pass, &tapogo.TapoOptions{})
		if err != nil {
			log.Fatal("Failed to connect to plug", err)
		}
		var tuvPlugState bool
		tuvPlugState, tuvPlug, err = plugIsOn(tuvPlug, config.Tapo.Plugs[1].IP, config.Tapo.Email, config.Tapo.Pass, 3)

		if (!tuvPlugState && (batterySoC == 0 || batterySoC == 100) && loadL3 < 1000 && tuvTemp < 67) ||
			(tuvPlugState && (batterySoC == 0 || batterySoC == 100) && loadL3 < 3000 && pvPower > 2000 && tuvTemp < 67) ||
			(!tuvPlugState && batterySoC > 40 && loadL3 < 1000 && pvPower > 2000 && tuvTemp < 67) ||
			(tuvPlugState && batterySoC > 40 && loadL3 < 3000 && pvPower > 2000 && tuvTemp < 67) {
			fmt.Printf("%v TUV: ON - PlugState: %v, SoC: %.1f, L3 Load: %.0f, Temp: %.1f, Panels: %.0f\n", time.Now().Format("2006-01-02 15:04:05"), tuvPlugState, batterySoC, loadL3, tuvTemp, pvPower)
			tuvPlug, err = plugTurnOn(tuvPlug, config.Tapo.Plugs[1].IP, config.Tapo.Email, config.Tapo.Pass, 3)
			if err != nil {
				log.Fatal("Failed to turn on tuv plug", err)
			}
		} else {
			fmt.Printf("%v TUV: OFF - PlugState: %v, SoC: %.1f, L3 Load: %.0f, Temp: %.1f, Panels: %.0f\n", time.Now().Format("2006-01-02 15:04:05"), tuvPlugState, batterySoC, loadL3, tuvTemp, pvPower)
			tuvPlug, err = plugTurnOff(tuvPlug, config.Tapo.Plugs[1].IP, config.Tapo.Email, config.Tapo.Pass, 3)
			if err != nil {
				log.Fatal("Failed to turn off tuv plug", err)
			}
		}

		time.Sleep(150 * time.Second)

		//var heaterPlug *tapogo.Tapo
		//heaterPlug, err = tapogo.NewTapo(config.Tapo.Plugs[0].IP, config.Tapo.Email, config.Tapo.Pass, &tapogo.TapoOptions{})
		//if err != nil {
		//	log.Fatal("Failed to connect to plug", err)
		//}
		//var heaterPlugState bool
		//heaterPlugState, heaterPlug, err = plugIsOn(heaterPlug, config.Tapo.Plugs[0].IP, config.Tapo.Email, config.Tapo.Pass, 3)

		//if (!tuvPlugState && !heaterPlugState && (batterySoC == 0 || batterySoC == 100) && loadL2 < 1000) ||
		//	(!tuvPlugState && heaterPlugState && (batterySoC == 0 || batterySoC == 100) && loadL2 < 3000 && pvPower > 2000) ||
		//	(!tuvPlugState && !heaterPlugState && batterySoC > 60 && loadL2 < 1000 && pvPower > 2000) ||
		//	(!tuvPlugState && heaterPlugState && batterySoC > 60 && loadL2 < 3000 && pvPower > 2000) {
		//	fmt.Printf("%v HEATER: ON - PlugState: %v, SoC: %.1f, L2 Load: %.0f, Panels: %.0f\n", time.Now().Format("2006-01-02 15:04:05"), heaterPlugState, batterySoC, loadL2, pvPower)
		//	heaterPlug, err = plugTurnOn(heaterPlug, config.Tapo.Plugs[0].IP, config.Tapo.Email, config.Tapo.Pass, 3)
		//	if err != nil {
		//		log.Fatal("Failed to turn on heater plug", err)
		//	}
		//} else {
		//	fmt.Printf("%v HEATER: OFF - PlugState: %v, SoC: %.1f, L2 Load: %.0f, Panels: %.0f\n", time.Now().Format("2006-01-02 15:04:05"), heaterPlugState, batterySoC, loadL2, pvPower)
		//	heaterPlug, err = plugTurnOff(heaterPlug, config.Tapo.Plugs[0].IP, config.Tapo.Email, config.Tapo.Pass, 3)
		//	if err != nil {
		//		log.Fatal("Failed to turn off heater plug", err)
		//	}
		//}

		time.Sleep(150 * time.Second)
	}
}
