# Nvidia Fan Control

A lightweight Linux utility for monitoring GPU temperatures and dynamically controlling NVIDIA GPU fan speeds using NVML.

## Requirements
- NVIDIA GPUs with NVML support
- NVIDIA drivers 520 or higher

## Build
```bash
go build -o nvidia-fan-control
```

## Configuration
edit the file `config.json` with the following structure
```
{
    "time_to_update": 3,
    "temperature_ranges": [
      { "min_temperature": 0, "max_temperature": 45, "fan_speed": 0, "hysteresis": 0 },
      { "min_temperature": 45, "max_temperature": 50, "fan_speed": 40, "hysteresis": 4 },
      { "min_temperature": 50, "max_temperature": 60, "fan_speed": 55, "hysteresis": 3 },
	  { "min_temperature": 60, "max_temperature": 70, "fan_speed": 70, "hysteresis": 3 },
      { "min_temperature": 70, "max_temperature": 80, "fan_speed": 80, "hysteresis": 3 },
      { "min_temperature": 80, "max_temperature": 85, "fan_speed": 90, "hysteresis": 3 },
      { "min_temperature": 85, "max_temperature": 95, "fan_speed": 95, "hysteresis": 3 },
      { "min_temperature": 95, "max_temperature": 999, "fan_speed": 100, "hysteresis": 0 }
    ]
  }
```

## Service
```bash
sudo vi /etc/systemd/system/nvidia-fan-control.service
```
update WorkingDirectory and set the path to your config file
```
[Unit]
Description=NVIDIA Fan Control Service
After=sysinit.target

[Service]
ExecStart=/usr/bin/sudo /path/to/nvidia-fan-control
WorkingDirectory=/path/to/your/config
StandardOutput=file:/var/log/nvidia-fan-control.log
StandardError=inherit
Restart=always
User=root
Group=root

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable nvidia-fan-control.service
sudo systemctl start nvidia-fan-control.service
sudo systemctl status nvidia-fan-control.service
```

### Check Logs
```bash
tail -f /var/log/nvidia-fan-control.log
```
