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
	TimeToUpdate      int                `json:"time_to_update"`
	TemperatureRanges []TemperatureRange `json:"temperature_ranges"`
}

type TemperatureRange struct {
	MinTemperature int `json:"min_temperature"`
	MaxTemperature int `json:"max_temperature"`
	FanSpeed       int `json:"fan_speed"`
	Hysteresis     int `json:"hysteresis"`
}

func loadConfig(file string) (Config, error) {
	var config Config
	data, err := os.ReadFile(file)
	if err != nil {
		return config, err
	}
	err = json.Unmarshal(data, &config)
	return config, err
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

		if prevRange.FanSpeed == -1 {
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

func loadConfiguration(configPath string) (Config, error) {
	config, err := loadConfig(configPath)
	if err != nil {
		return config, fmt.Errorf("failed to load config %s: %w", configPath, err)
	}

	if config.TimeToUpdate <= 0 {
		log.Printf("WARN: time_to_update (%d) is invalid, defaulting to 5 seconds.", config.TimeToUpdate)
		config.TimeToUpdate = 5
	}
	log.Println("INFO: Configuration loaded.")
	return config, nil
}

func initializeNVML() (cleanupFunc func(), err error) {
	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("unable to initialize NVML: %v", nvml.ErrorString(ret))
	}

	cleanupFunc = func() {
		log.Println("INFO: Shutting down NVML...")
		ret := nvml.Shutdown()
		if ret != nvml.SUCCESS {
			log.Printf("ERROR: Unable to shutdown NVML cleanly: %v", nvml.ErrorString(ret))
		} else {
			log.Println("INFO: NVML Shutdown complete.")
		}
	}

	return cleanupFunc, nil
}

func initializeDevices() (count int, fanCounts []int, prevTemps []int, prevFanSpeeds [][]int, err error) {
	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return 0, nil, nil, nil, fmt.Errorf("unable to get NVIDIA device count: %v", nvml.ErrorString(ret))
	}
	if count == 0 {
		return 0, nil, nil, nil, fmt.Errorf("no NVIDIA devices found")
	}
	log.Printf("INFO: Found %d NVIDIA device(s).", count)

	fanCounts = make([]int, count)
	prevTemps = make([]int, count)
	prevFanSpeeds = make([][]int, count)
	initializedDevices := 0

	for i := 0; i < count; i++ {
		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			log.Printf("WARN: Unable to get handle for device %d: %v. Skipping device.", i, nvml.ErrorString(ret))
			fanCounts[i] = 0
			continue
		}

		var numFansInt int
		numFansInt, ret = nvml.DeviceGetNumFans(device)
		if ret != nvml.SUCCESS {
			log.Printf("WARN: Unable to get fan count for device %d: %v. Assuming 0 fans or fan control not supported.", i, nvml.ErrorString(ret))
			fanCounts[i] = 0
			continue
		}
		fanCounts[i] = numFansInt

		if fanCounts[i] <= 0 {
			log.Printf("INFO: Device %d reports %d controllable fans. Skipping fan initialization.", i, fanCounts[i])
			continue
		}

		log.Printf("INFO: Device %d has %d controllable fan(s). Initializing state.", i, fanCounts[i])
		prevFanSpeeds[i] = make([]int, fanCounts[i])

		temp, ret := nvml.DeviceGetTemperature(device, nvml.TEMPERATURE_GPU)
		if ret == nvml.SUCCESS {
			prevTemps[i] = int(temp)
		} else {
			log.Printf("WARN: Failed to get initial temperature for device %d: %v. Using 0.", i, nvml.ErrorString(ret))
			prevTemps[i] = 0
		}

		for fanIdx := 0; fanIdx < fanCounts[i]; fanIdx++ {
			speed, ret := nvml.DeviceGetFanSpeed_v2(device, fanIdx)
			if ret == nvml.SUCCESS {
				prevFanSpeeds[i][fanIdx] = int(speed)
			} else {
				speedLegacy, retLegacy := nvml.DeviceGetFanSpeed(device)
				if retLegacy == nvml.SUCCESS && fanIdx == 0 {
					log.Printf("WARN: Using legacy DeviceGetFanSpeed for initial speed for device %d Fan %d.", i, fanIdx)
					prevFanSpeeds[i][fanIdx] = int(speedLegacy)
				} else {
					log.Printf("WARN: Failed to get initial speed for device %d Fan %d using v2 (%v) or legacy (%v). Using 0.", i, fanIdx, nvml.ErrorString(ret), nvml.ErrorString(retLegacy))
					prevFanSpeeds[i][fanIdx] = 0
				}
			}
		}
		log.Printf("INFO: Initial state for device %d: Temp=%d°C, Fan Speeds=%v%%", i, prevTemps[i], prevFanSpeeds[i])
		initializedDevices++
	}

	if initializedDevices == 0 && count > 0 {
		return count, fanCounts, prevTemps, prevFanSpeeds, fmt.Errorf("found %d devices, but failed to initialize any for monitoring/control", count)
	}

	log.Printf("INFO: Device initialization complete. Monitoring %d devices.", initializedDevices)
	return count, fanCounts, prevTemps, prevFanSpeeds, nil
}

func runMonitoringLoop(config Config, count int, fanCounts []int, prevTemps []int, prevFanSpeeds [][]int) {
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
				log.Printf("ERROR: Unable to get handle for device %d during update: %v. Skipping cycle for this device.", i, nvml.ErrorString(ret))
				continue
			}

			temp, ret := nvml.DeviceGetTemperature(device, nvml.TEMPERATURE_GPU)
			if ret != nvml.SUCCESS {
				log.Printf("ERROR: Unable to get temperature for device %d: %v. Skipping cycle for this device.", i, nvml.ErrorString(ret))
				continue
			}
			tempInt := int(temp)

			for fanIdx := 0; fanIdx < fanCounts[i]; fanIdx++ {
				newFanSpeed := getFanSpeedForTemperature(tempInt, prevFanSpeeds[i][fanIdx], config.TemperatureRanges)

				if newFanSpeed != prevFanSpeeds[i][fanIdx] {
					ret = nvml.DeviceSetFanControlPolicy(device, fanIdx, nvml.FAN_POLICY_MANUAL)
					if ret != nvml.SUCCESS && ret != nvml.ERROR_NOT_SUPPORTED {
						log.Printf("ERROR: Unable to set manual fan control policy for GPU %d Fan %d: %v", i, fanIdx, nvml.ErrorString(ret))
						continue
					} else if ret == nvml.ERROR_NOT_SUPPORTED {
						log.Printf("WARN: Manual fan control policy not supported for GPU %d Fan %d. Cannot set speed.", i, fanIdx)
						continue
					}

					ret = nvml.DeviceSetFanSpeed_v2(device, fanIdx, newFanSpeed)
					if ret != nvml.SUCCESS {
						log.Printf("ERROR: Unable to set fan speed for GPU %d Fan %d to %d%%: %v", i, fanIdx, newFanSpeed, nvml.ErrorString(ret))
						continue
					}

					log.Printf("INFO: Updated GPU %d Fan %d: Temp=%d°C, PrevSpeed=%d%%, NewSpeed=%d%%", i, fanIdx, tempInt, prevFanSpeeds[i][fanIdx], newFanSpeed)
					prevFanSpeeds[i][fanIdx] = newFanSpeed
				}
			}
			prevTemps[i] = tempInt
		}
	}
}

func main() {
	logFile, err := setupLogging("/var/log/nvidia-fan-control.log")
	if err != nil {
		log.Printf("FATAL: %v", err)
		os.Exit(1)
	}
	defer logFile.Close()

	config, err := loadConfiguration("config.json")
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	nvmlCleanup, err := initializeNVML()
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}
	defer nvmlCleanup()

	count, fanCounts, prevTemps, prevFanSpeeds, err := initializeDevices()
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

	runMonitoringLoop(config, count, fanCounts, prevTemps, prevFanSpeeds)

	log.Println("INFO: Monitoring loop finished unexpectedly.")
}
