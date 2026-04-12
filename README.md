# xfx1-fancontrol

Small Go daemon that controls a PWM fan based on NVIDIA GPU temperature.

Built for the case where you've mounted an aftermarket fan (on a motherboard
`CHA_FAN` header) on top of a GPU and want it to ramp with GPU temperature
instead of sitting at a BIOS-defined curve that knows nothing about the GPU.

## How it works

- Reads GPU temp from `nvidia-smi` (the proprietary driver doesn't expose
  temperature via hwmon, so there's no sysfs alternative).
- Writes PWM duty cycle (0–255) to the motherboard super-IO chip via
  `/sys/class/hwmon/hwmonX/pwmN`.
- On startup: saves the current `pwmN_enable` value, then sets it to `1`
  (manual mode).
- On `SIGINT`/`SIGTERM`: restores the original `pwmN_enable` so the BIOS
  takes over again.
- Linearly interpolates PWM from a user-defined temperature → PWM curve.

No external Go dependencies — stdlib only, plus `nvidia-smi` on `PATH`.

## Build

```sh
go-task build    # -> _out/fancontrol
go-task run      # builds + runs in dry-run mode
go-task clean
go test ./...
```

## Configuration

See [deploy/fancontrol.conf](deploy/fancontrol.conf) for an annotated example.

Minimal config:

```ini
temp_source = nvidia-smi
fan_pwm     = /sys/class/hwmon/hwmon3/pwm4
fan_rpm     = /sys/class/hwmon/hwmon3/fan4_input
interval    = 3

[curve]
30 = 60
80 = 240
85 = 255
```

- `temp_source`: `nvidia-smi` or a sysfs hwmon path (millidegrees).
- `gpu_index`: optional. Required only on multi-GPU systems — the program
  fails hard if `nvidia-smi` reports more than one GPU without this set.
- `fan_pwm`: path to the PWM control file. The `_enable` sibling is derived
  automatically.
- `fan_rpm`: optional RPM sensor for logging.
- `[curve]`: `temp_celsius = pwm_value` pairs (0–255). Linearly interpolated.
  Values are clamped below the first point and above the last.

### Finding the right fan header

Motherboard header names (e.g. `CHA_FAN3`) don't map directly to hwmon fan
numbers. Identify which `pwmN` drives your fan by temporarily setting one to
a low value and listening/watching RPM:

```sh
sudo sh -c 'echo 1  > /sys/class/hwmon/hwmon3/pwm4_enable'
sudo sh -c 'echo 30 > /sys/class/hwmon/hwmon3/pwm4'
cat /sys/class/hwmon/hwmon3/fan4_input       # should drop noticeably
sudo sh -c 'echo 5  > /sys/class/hwmon/hwmon3/pwm4_enable'   # restore BIOS
```

If the target fan didn't slow down, repeat with a different `pwmN`.

## Install (systemd)

```sh
sudo install -Dm755 _out/fancontrol /usr/local/bin/xfx1-fancontrol
sudo install -Dm644 deploy/fancontrol.conf /etc/xfx1-fancontrol/fancontrol.conf
sudo install -Dm644 deploy/xfx1-fancontrol.service /etc/systemd/system/xfx1-fancontrol.service
sudo systemctl daemon-reload
sudo systemctl enable --now xfx1-fancontrol.service
```

## Flags

- `--config PATH` — path to config file (default `/etc/xfx1-fancontrol/fancontrol.conf`).
- `--dry-run` — read temps and log target PWM without writing to sysfs.
- `--verbose` — log every tick, bypassing the normal log policy (for debugging).

## Logging policy

A fan controller polled every few seconds would drown the system journal in
noise, so the default is event-driven:

- **At or above 90°C** — log every tick where temp, PWM, or RPM changed vs.
  the previous tick (dense logging when things are hot).
- **Below 90°C** — log only when temperature has moved ≥ 10°C from the last
  logged temperature, **or** when one hour has passed since the last log line
  (heartbeat / liveness proof). Either event resets the heartbeat timer.
- Errors, startup, and shutdown messages always log.

Pass `--verbose` to get a line on every tick (useful when tuning the curve).

## License

AGPL-3.0-or-later. See [LICENSE](LICENSE).
