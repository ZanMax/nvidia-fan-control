# Nvidia Fan Control
forked from ZanMax/nvidia-fan-control and added multiple GPU and Multiple fan controller support.

A lightweight Linux utility for monitoring GPU temperatures and dynamically controlling NVIDIA GPU fan speeds using NVML.

## Requirements
- NVIDIA GPUs with NVML support
- NVIDIA drivers 520 or higher

## Build
```bash
go build -o nvidia_fan_control
```

## Installation
```bash
sudo mv nvidia_fan_control /usr/local/bin/
```

## Configuration
edit the file `config.json` with the following structure
```
{
    "time_to_update": 5,
    "temperature_ranges": [
      { "min_temperature": 0, "max_temperature": 40, "fan_speed": 30, "hysteresis": 3 },
      { "min_temperature": 40, "max_temperature": 60, "fan_speed": 40, "hysteresis": 3 },
      { "min_temperature": 60, "max_temperature": 80, "fan_speed": 70, "hysteresis": 3 },
      { "min_temperature": 80, "max_temperature": 100, "fan_speed": 100, "hysteresis": 3 },
      { "min_temperature": 100, "max_temperature": 200, "fan_speed": 100, "hysteresis": 0 }
    ]
  }
```

## Service
```bash
sudo nano /etc/systemd/system/nvidia_fan_control.service
```
update WorkingDirectory and set the path to your config file
```
[Unit]
Description=NVIDIA Fan Control Service
After=network.target

[Service]
ExecStart=/usr/bin/sudo /usr/local/bin/nvidia_fan_control
WorkingDirectory=/path/to/your/config
StandardOutput=file:/var/log/nvidia_fan_control.log
StandardError=file:/var/log/nvidia_fan_control_error.log
Restart=always
User=root
Group=root

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable nvidia_fan_control.service
sudo systemctl start nvidia_fan_control.service
sudo systemctl status nvidia_fan_control.service
```

### Check Logs
```bash
sudo tail -f /var/log/nvidia_fan_control.log
```
