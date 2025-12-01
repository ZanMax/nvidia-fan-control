package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

type Config struct {
	TimeToUpdate	int		`json:"time_to_update"`
	TemperatureRanges	[]TemperatureRange	`json:"temperature_ranges"`
}

type TemperatureRange struct {
	MinTemperature	int	`json:"min_temperature"`
	MaxTemperature	int	`json:"max_temperature"`
	FanSpeed	int	`json:"fan_speed"`
	Hysteresis	int	`json:"hysteresis"`
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func getFanSpeedForTemperature(temp int, prevSpeed int, ranges []TemperatureRange) int {
	idealSpeed := prevSpeed

	for _, r := range ranges {
		if temp > r.MinTemperature && temp <= r.MaxTemperature {
			idealSpeed = r.FanSpeed
			break
		}
	}

	if idealSpeed > prevSpeed {
		return idealSpeed
	}

	if idealSpeed < prevSpeed {
		var prevRange TemperatureRange
		for _, r := range ranges {
			if r.FanSpeed == prevSpeed {
				prevRange = r
				break
			}
		}

		if prevRange.FanSpeed == 0 && prevRange.MinTemperature == 0 && prevRange.Hysteresis == 0 {
			return idealSpeed
		}

		if temp <= prevRange.MinTemperature-prevRange.Hysteresis {
			return idealSpeed
		}

		return prevSpeed
	}

	return prevSpeed
}

func setupLogging(logFilePath string) (*os.File, error) {
	logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file %s: %w", logFilePath, err)
	}
	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	return logFile, nil
}

func loadConfig(file string) (Config, error) {
	var config Config
	data, err := os.ReadFile(file)
	if err != nil {
		return config, err
	}
	err = json.Unmarshal(data, &config)

	if config.TimeToUpdate <= 0 {
		log.Printf("WARN: time_to_update (%d) is invalid, defaulting to 5 seconds.", config.TimeToUpdate)
		config.TimeToUpdate = 5
	}
	log.Println("INFO: Configuration loaded.")
	return config, err
}

func initNVML() (func(), error) {
	if ret := nvml.Init(); ret != nvml.SUCCESS {
		return nil, fmt.Errorf("unable to initialize NVML: %v", nvml.ErrorString(ret))
	}
	return func() {
		if ret := nvml.Shutdown(); ret != nvml.SUCCESS {
			log.Printf("ERROR: Unable to shutdown NVML cleanly: %v", nvml.ErrorString(ret))
		}
	}, nil
}

func initDevices() (int, []int, [][]int, error) {
	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return 0, nil, nil, fmt.Errorf("unable to get NVIDIA device count: %v", nvml.ErrorString(ret))
	}
	if count == 0 {
		return 0, nil, nil, fmt.Errorf("no NVIDIA devices found")
	}
	log.Printf("INFO: Found %d NVIDIA device(s).", count)

	fanCounts := make([]int, count)
	prevFanSpeeds := make([][]int, count)
	initializedDevices := 0

	for i := 0; i < count; i++ {
		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			log.Printf("WARN: Unable to get handle for device %d: %v. Skipping.", i, nvml.ErrorString(ret))
			continue
		}

		numFans, ret := nvml.DeviceGetNumFans(device)
		if ret != nvml.SUCCESS || numFans <= 0 {
			log.Printf("INFO: Device %d reports 0 controllable fans or control not supported. Skipping.", i)
			continue
		}
		fanCounts[i] = numFans
		prevFanSpeeds[i] = make([]int, numFans)

		temp, _ := nvml.DeviceGetTemperature(device, nvml.TEMPERATURE_GPU)

		for fanIdx := 0; fanIdx < numFans; fanIdx++ {
			speed, ret := nvml.DeviceGetFanSpeed_v2(device, fanIdx)
			if ret != nvml.SUCCESS {
				speedLegacy, retLegacy := nvml.DeviceGetFanSpeed(device)
				if retLegacy == nvml.SUCCESS && fanIdx == 0 {
					speed = speedLegacy
				} else {
					log.Printf("WARN: Failed to get initial speed for device %d Fan %d. Using 0.", i, fanIdx)
					speed = 0
				}
			}
			prevFanSpeeds[i][fanIdx] = int(speed)
		}
		log.Printf("INFO: Initial state for device %d: Temp=%d°C, Fan Speeds=%v%%", i, int(temp), prevFanSpeeds[i])
		initializedDevices++
	}

	if initializedDevices == 0 && count > 0 {
		return count, fanCounts, prevFanSpeeds, fmt.Errorf("found %d devices, but failed to initialize any for fan control", count)
	}
	return count, fanCounts, prevFanSpeeds, nil
}

func runMonitoringLoop(config Config, count int, fanCounts []int, prevFanSpeeds [][]int) {
	log.Println("INFO: Starting monitoring loop...")
	ticker := time.NewTicker(time.Duration(config.TimeToUpdate) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		for i := 0; i < count; i++ {
			if fanCounts[i] == 0 {
				continue
			}

			device, ret := nvml.DeviceGetHandleByIndex(i)
			if ret != nvml.SUCCESS {
				log.Printf("ERROR: Failed to get handle for device %d: %v. Skipping cycle.", i, nvml.ErrorString(ret))
				continue
			}

			temp, ret := nvml.DeviceGetTemperature(device, nvml.TEMPERATURE_GPU)
			if ret != nvml.SUCCESS {
				log.Printf("ERROR: Failed to get temperature for device %d: %v. Skipping cycle.", i, nvml.ErrorString(ret))
				continue
			}
			tempInt := int(temp)

			for fanIdx := 0; fanIdx < fanCounts[i]; fanIdx++ {
				newFanSpeed := getFanSpeedForTemperature(tempInt, prevFanSpeeds[i][fanIdx], config.TemperatureRanges)

				if newFanSpeed != prevFanSpeeds[i][fanIdx] {
					if ret := nvml.DeviceSetFanControlPolicy(device, fanIdx, nvml.FAN_POLICY_MANUAL); ret != nvml.SUCCESS && ret != nvml.ERROR_NOT_SUPPORTED {
						log.Printf("ERROR: Failed to set manual policy for GPU %d Fan %d: %v", i, fanIdx, nvml.ErrorString(ret))
						continue
					}

					if ret := nvml.DeviceSetFanSpeed_v2(device, fanIdx, newFanSpeed); ret != nvml.SUCCESS {
						log.Printf("ERROR: Failed to set speed for GPU %d Fan %d to %d%%: %v", i, fanIdx, newFanSpeed, nvml.ErrorString(ret))
						continue
					}

					log.Printf("INFO: Updated GPU %d Fan %d: Temp=%d°C, PrevSpeed=%d%%, NewSpeed=%d%%", i, fanIdx, tempInt, prevFanSpeeds[i][fanIdx], newFanSpeed)
					prevFanSpeeds[i][fanIdx] = newFanSpeed
				}
			}
		}
	}
}

func main() {
	logFile, err := setupLogging("/var/log/nvidia-fan-control.log")
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}
	defer logFile.Close()

	config, err := loadConfig("config.json")
	if err != nil {
		log.Fatalf("FATAL: Failed to load config: %v", err)
	}

	nvmlCleanup, err := initNVML()
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}
	defer nvmlCleanup()

	count, fanCounts, prevFanSpeeds, err := initDevices()
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	hasControllableFans := false
	for _, fc := range fanCounts {
		if fc > 0 {
			hasControllableFans = true
			break
		}
	}

	if !hasControllableFans {
		log.Println("INFO: No devices with controllable fans were found or initialized. Exiting.")
		return
	}

	runMonitoringLoop(config, count, fanCounts, prevFanSpeeds)

	log.Println("INFO: Monitoring loop finished unexpectedly.")
}
