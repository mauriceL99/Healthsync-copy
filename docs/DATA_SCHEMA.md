# HealthBatch Schema v1

The plaintext inside every envelope is one UTF-8 JSON object of type **HealthBatch**.
The agent treats it as **untrusted input**. It keeps only the fields listed here,
only finite numbers within the stated ranges, and only strings that match the stated
patterns. Everything else is dropped. Free text never reaches the agent, which blocks
prompt injection through health data.

The Go implementation of this catalog is `internal/healthdata/catalog.go`.
The iOS implementation is `ios/HealthBridge/Health/MetricCatalog.swift`.

## HealthBatch

```json
{
  "schema": 1,
  "generated_at": "2026-10-13T08:00:00Z",
  "timezone": "Europe/Berlin",
  "source": { "app_version": "1.0.0", "os_version": "18.1", "device_model": "iPhone16,1" },
  "days": {
    "2026-10-12": {
      "activity": { "steps": 9214, "active_energy_kcal": 612.4, "exercise_min": 41 },
      "heart":    { "resting_bpm": 54, "hrv_sdnn_ms": 47.9, "avg_bpm": 71.2, "min_bpm": 48, "max_bpm": 164 },
      "sleep":    { "asleep_min": 431, "deep_min": 62, "rem_min": 98, "core_min": 271, "awake_min": 18,
                    "in_bed_min": 452, "bedtime": "2026-10-11T21:58:00Z", "wake_time": "2026-10-12T05:30:00Z",
                    "sessions": [ { "stage": "core", "start": "2026-10-11T22:05:00Z", "end": "2026-10-11T23:10:00Z" } ] },
      "body":     { "weight_kg": 78.4 },
      "vitals":   { "respiratory_rate_bpm": 14.1, "oxygen_saturation_pct": 97 },
      "nutrition":{ "water_ml": 2100 },
      "mindfulness": { "mindful_min": 10 }
    }
  },
  "workouts": [
    {
      "id": "6f1c0b8e-3d1a-4c55-9a3e-7a6c8d1e2f30",
      "type": "running",
      "start": "2026-10-12T16:02:11Z",
      "end": "2026-10-12T16:48:40Z",
      "duration_s": 2789,
      "energy_kcal": 498.2,
      "distance_m": 8040.5,
      "elevation_gain_m": 63,
      "indoor": false,
      "source": "Apple Watch",
      "heart_rate": {
        "avg_bpm": 152.3, "min_bpm": 96, "max_bpm": 178, "recovery_1min_bpm": 31,
        "max_reference_bpm": 190,
        "zones": [ { "zone": 1, "lower_bpm": 95, "upper_bpm": 114, "seconds": 120 } ],
        "samples": [ { "t": 0, "bpm": 98 }, { "t": 5, "bpm": 104 } ]
      }
    }
  ]
}
```

### Top level

| Field | Type | Rule |
| --- | --- | --- |
| `schema` | int | must be `1` |
| `generated_at` | timestamp | RFC 3339 |
| `timezone` | string | `^[A-Za-z][A-Za-z0-9_+./-]{1,63}$` (IANA name) |
| `source.app_version` | string | `^[0-9A-Za-z.+-]{1,32}$` |
| `source.os_version` | string | `^[0-9A-Za-z.+-]{1,32}$` |
| `source.device_model` | string | `^[A-Za-z]{2,16}[0-9]{0,3}(,[0-9]{1,3})?$` (hardware identifier such as `iPhone16,1`) |
| `days` | object | keys `YYYY-MM-DD` (device-local calendar day), ≤ 4000 days |
| `workouts` | array | ≤ 2000 items |

## Day metrics

Days are local calendar days of the device time zone. Sleep is attributed to the day
on which the user **wakes up**. The window runs from the previous day 18:00 to the
day itself 18:00, local time.

Aggregation: **sum** = daily total, **avg** = daily mean of samples,
**min/max** = daily extreme, **latest** = most recent sample of the day,
**summary** = value from `HKActivitySummary`.

### `activity`

| Key | HealthKit | Unit | Agg | Range |
| --- | --- | --- | --- | --- |
| `steps` | `stepCount` | count | sum | 0–200 000 |
| `distance_walking_running_m` | `distanceWalkingRunning` | m | sum | 0–300 000 |
| `distance_cycling_m` | `distanceCycling` | m | sum | 0–1 000 000 |
| `distance_swimming_m` | `distanceSwimming` | m | sum | 0–100 000 |
| `active_energy_kcal` | `activeEnergyBurned` | kcal | sum | 0–20 000 |
| `basal_energy_kcal` | `basalEnergyBurned` | kcal | sum | 0–10 000 |
| `flights_climbed` | `flightsClimbed` | count | sum | 0–2 000 |
| `exercise_min` | `appleExerciseTime` | min | sum | 0–1 440 |
| `stand_min` | `appleStandTime` | min | sum | 0–1 440 |
| `stand_hours` | `HKActivitySummary.appleStandHours` | h | summary | 0–24 |
| `move_goal_kcal` | `HKActivitySummary.activeEnergyBurnedGoal` | kcal | summary | 0–20 000 |
| `exercise_goal_min` | `HKActivitySummary.appleExerciseTimeGoal` | min | summary | 0–1 440 |
| `stand_goal_hours` | `HKActivitySummary.appleStandHoursGoal` | h | summary | 0–24 |

### `heart`

| Key | HealthKit | Unit | Agg | Range |
| --- | --- | --- | --- | --- |
| `resting_bpm` | `restingHeartRate` | bpm | avg | 20–250 |
| `walking_avg_bpm` | `walkingHeartRateAverage` | bpm | avg | 20–250 |
| `avg_bpm` | `heartRate` | bpm | avg | 20–260 |
| `min_bpm` | `heartRate` | bpm | min | 20–260 |
| `max_bpm` | `heartRate` | bpm | max | 20–260 |
| `hrv_sdnn_ms` | `heartRateVariabilitySDNN` | ms | avg | 0–500 |
| `vo2max_ml_kg_min` | `vo2Max` | ml/kg·min | latest | 5–100 |
| `recovery_1min_bpm` | `heartRateRecoveryOneMinute` | bpm | avg | 0–150 |

### `sleep` (from `sleepAnalysis`)

| Key | Unit | Range / Pattern |
| --- | --- | --- |
| `in_bed_min` | min | 0–1 440 |
| `asleep_min` | min (core + deep + rem + unspecified) | 0–1 440 |
| `core_min`, `deep_min`, `rem_min`, `awake_min`, `unspecified_min` | min | 0–1 440 |
| `bedtime` | timestamp | first asleep/in-bed start |
| `wake_time` | timestamp | last asleep end |
| `sessions[]` | ≤ 300 | `{stage, start, end}`; `stage` ∈ `in_bed, asleep_unspecified, core, deep, rem, awake` |

### `body` (latest of day)

| Key | HealthKit | Unit | Range |
| --- | --- | --- | --- |
| `weight_kg` | `bodyMass` | kg | 1–500 |
| `body_fat_pct` | `bodyFatPercentage` | % (0–100) | 1–80 |
| `bmi` | `bodyMassIndex` | – | 5–100 |
| `lean_body_mass_kg` | `leanBodyMass` | kg | 1–300 |
| `height_cm` | `height` | cm | 30–260 |
| `waist_cm` | `waistCircumference` | cm | 20–300 |

### `vitals` (daily average)

| Key | HealthKit | Unit | Range |
| --- | --- | --- | --- |
| `respiratory_rate_bpm` | `respiratoryRate` | breaths/min | 2–80 |
| `oxygen_saturation_pct` | `oxygenSaturation` | % (0–100) | 50–100 |
| `body_temperature_c` | `bodyTemperature` | °C | 30–45 |
| `sleeping_wrist_temperature_c` | `appleSleepingWristTemperature` | °C | 25–45 |
| `blood_pressure_systolic_mmhg` | `bloodPressureSystolic` | mmHg | 40–300 |
| `blood_pressure_diastolic_mmhg` | `bloodPressureDiastolic` | mmHg | 20–200 |
| `blood_glucose_mg_dl` | `bloodGlucose` | mg/dL | 10–1 000 |

### `nutrition` (daily sum)

| Key | HealthKit | Unit | Range |
| --- | --- | --- | --- |
| `energy_kcal` | `dietaryEnergyConsumed` | kcal | 0–20 000 |
| `protein_g` | `dietaryProtein` | g | 0–1 000 |
| `carbohydrates_g` | `dietaryCarbohydrates` | g | 0–2 000 |
| `fat_g` | `dietaryFatTotal` | g | 0–1 000 |
| `fiber_g` | `dietaryFiber` | g | 0–500 |
| `sugar_g` | `dietarySugar` | g | 0–1 000 |
| `water_ml` | `dietaryWater` | ml | 0–20 000 |
| `caffeine_mg` | `dietaryCaffeine` | mg | 0–5 000 |

### `mindfulness`

| Key | HealthKit | Unit | Range |
| --- | --- | --- | --- |
| `mindful_min` | `mindfulSession` (sum of durations) | min | 0–1 440 |

## Workouts

| Field | Type | Rule |
| --- | --- | --- |
| `id` | string | lowercase or uppercase UUID |
| `type` | string | `^[a-z][a-z0-9_]{1,47}$`, snake_case of `HKWorkoutActivityType` (`running`, `cycling`, `traditional_strength_training`, …) |
| `start`, `end` | timestamp | `end ≥ start`, duration ≤ 48 h |
| `duration_s` | number | 0–172 800 |
| `energy_kcal` | number | 0–20 000 |
| `distance_m` | number | 0–1 000 000 |
| `elevation_gain_m` | number | 0–20 000 |
| `indoor` | bool | |
| `source` | string | `^[A-Za-z0-9][A-Za-z0-9 .'()&+-]{0,63}$` |
| `heart_rate.avg_bpm`, `min_bpm`, `max_bpm` | number | 20–260 |
| `heart_rate.recovery_1min_bpm` | number | 0–150 |
| `heart_rate.max_reference_bpm` | number | 100–240, the maximum heart rate used for the zones |
| `heart_rate.zones[]` | ≤ 5 | `{zone: 1–5, lower_bpm ≥ 0, upper_bpm ≤ 260, lower ≤ upper, seconds (0–172 800)}` |
| `heart_rate.samples[]` | ≤ 4 000 | `{t: seconds since start (0–172 800), bpm: 20–260}`, at most one sample per 5 s |

Heart-rate zones use percentages of `max_reference_bpm`:
Z1 50–60 %, Z2 60–70 %, Z3 70–80 %, Z4 80–90 %, Z5 90–100 %.
`max_reference_bpm` is `220 − age` if the date of birth is shared, otherwise the highest
heart rate observed in the last 365 days, with a fallback of 190.

The day of a workout is the local calendar day of `start`.
