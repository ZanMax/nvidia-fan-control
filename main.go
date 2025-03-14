package main

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// Config structure to match the provided JSON
type Config struct {
	TimeToUpdate      int                `json:"time_to_update"`
	TemperatureRanges []TemperatureRange `json:"temperature_ranges"`
}

type TemperatureRange struct {
	MinTemperature int `json:"min_temperature"`
	MaxTemperature int `json:"max_temperature"`
	FanSpeed       int `json:"fan_speed"`
	Hysteresis     int `json:"hysteresis"`
}

// LoadConfig reads the JSON config file
func loadConfig(file string) (Config, error) {
	var config Config
	data, err := os.ReadFile(file)
	if err != nil {
		return config, err
	}
	err = json.Unmarshal(data, &config)
	return config, err
}

// Absolute difference function
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// GetFanSpeedForTemperature determines the appropriate fan speed based on temperature and hysteresis
func getFanSpeedForTemperature(temp, prevTemp, prevSpeed int, ranges []TemperatureRange) int {
	for _, r := range ranges {
		if temp > r.MinTemperature && temp <= r.MaxTemperature {
			if abs(temp-prevTemp) >= r.Hysteresis || prevSpeed != r.FanSpeed {
				return r.FanSpeed
			}
		}
	}
	return prevSpeed
}

func main() {
	logFile, err := os.OpenFile("/var/log/nvidia_fan_control.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open log file.: %v", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	config, err := loadConfig("config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		log.Fatalf("Unable to initialize NVML: %v", nvml.ErrorString(ret))
	}
	defer nvml.Shutdown()

	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		log.Fatalf("Unable to get device count: %v", nvml.ErrorString(ret))
	}

	prevTemps := make([][]int, count)
	prevFanSpeeds := make([][]int, count)

	for {
		for i := 0; i < count; i++ {
			device, ret := nvml.DeviceGetHandleByIndex(i)
			if ret != nvml.SUCCESS {
				log.Printf("Unable to get device at index %d: %v", i, nvml.ErrorString(ret))
				continue
			}

			// Get fan count per GPU
			fanCount := 2 // Default to 2 if the card has dual fans (adjust as needed)
			if len(prevTemps[i]) == 0 {
				prevTemps[i] = make([]int, fanCount)
				prevFanSpeeds[i] = make([]int, fanCount)
			}

			for fanIdx := 0; fanIdx < fanCount; fanIdx++ {
				temp, ret := nvml.DeviceGetTemperature(device, nvml.TEMPERATURE_GPU)
				if ret != nvml.SUCCESS {
					log.Printf("Unable to get temperature for GPU %d: %v", i, nvml.ErrorString(ret))
					continue
				}

				tempInt := int(temp)
				newFanSpeed := getFanSpeedForTemperature(tempInt, prevTemps[i][fanIdx], prevFanSpeeds[i][fanIdx], config.TemperatureRanges)

				if newFanSpeed != prevFanSpeeds[i][fanIdx] {
					ret = nvml.DeviceSetFanControlPolicy(device, fanIdx, 1)
					if ret != nvml.SUCCESS {
						log.Printf("Unable to set manual fan control policy for GPU %d Fan %d: %v", i, fanIdx, nvml.ErrorString(ret))
						continue
					}

					ret = nvml.DeviceSetFanSpeed_v2(device, fanIdx, newFanSpeed)
					if ret != nvml.SUCCESS {
						log.Printf("Unable to set fan speed for GPU %d Fan %d: %v", i, fanIdx, nvml.ErrorString(ret))
						continue
					}

					log.Printf("Updated GPU %d Fan %d: Temp=%d°C, Fan Speed=%d%%", i, fanIdx, tempInt, newFanSpeed)
					prevFanSpeeds[i][fanIdx] = newFanSpeed
				}

				prevTemps[i][fanIdx] = tempInt
			}
		}

		time.Sleep(time.Duration(config.TimeToUpdate) * time.Second)
	}
}
