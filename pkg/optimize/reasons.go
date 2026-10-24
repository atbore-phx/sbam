// Package optimize builds the hourly dispatch grid and computes the
// minimum-expected-cost battery plan (SPEC-0001, ADR-0002). It is pure: no
// I/O, no logging, and the clock is always passed in.
package optimize

// Reason is a stable decision reason code (UX design §4). Codes never embed
// numbers; numeric detail travels in the opt_* state fields and the plan.
type Reason string

// Fallback codes: inverter defaults are restored (AC-16).
const (
	ReasonFallbackHAUnreachable       Reason = "optimizer_fallback_ha_unreachable"
	ReasonFallbackHAAuthFailed        Reason = "optimizer_fallback_ha_auth_failed"
	ReasonFallbackPriceEntityNotFound Reason = "optimizer_fallback_price_entity_not_found"
	ReasonFallbackPriceFormat         Reason = "optimizer_fallback_price_format"
	ReasonFallbackMissingPrices       Reason = "optimizer_fallback_missing_prices"
	ReasonFallbackPriceInconsistent   Reason = "optimizer_fallback_price_inconsistent"
	ReasonFallbackImplausibleInput    Reason = "optimizer_fallback_implausible_input"
	ReasonFallbackSocUnavailable      Reason = "optimizer_fallback_soc_unavailable"
	ReasonFallbackSolverError         Reason = "optimizer_fallback_solver_error"
	ReasonFallbackInvalidPlan         Reason = "optimizer_fallback_invalid_plan"
	ReasonFallbackInverterLimits      Reason = "optimizer_fallback_inverter_limits"
	ReasonFallbackModbusWriteFailed   Reason = "optimizer_fallback_modbus_write_failed"
	ReasonWatchdogMinSoc              Reason = "optimizer_watchdog_min_soc"
)

// Override codes: the plan is computed and published, nothing is written.
const (
	ReasonPaused         Reason = "optimizer_paused"
	ReasonManualOverride Reason = "optimizer_manual_override"
)

// Guard codes: the applied action differs from the plan (SPEC-0002).
const (
	// ReasonEVGuardHold: the EV charges in a self_consume slot, so hold is
	// applied to keep the battery from discharging into the car.
	ReasonEVGuardHold Reason = "optimizer_ev_guard_hold"
)

// Degraded-input codes: the plan is still applied.
const (
	ReasonConsumptionStaticFallback Reason = "optimizer_consumption_static_fallback"
	ReasonConsumptionEntityNotFound Reason = "optimizer_consumption_entity_not_found"
	ReasonEVEntityNotFound          Reason = "optimizer_ev_entity_not_found"
	ReasonPVForecastMissing         Reason = "optimizer_pv_forecast_missing"
	ReasonPVForecastStale           Reason = "optimizer_pv_forecast_stale"
)

// Action codes: exactly one per run, for the applied slot.
const (
	ReasonChargeAheadOfPeak          Reason = "optimizer_charge_ahead_of_peak"
	ReasonChargeNegativePrice        Reason = "optimizer_charge_negative_price"
	ReasonHoldForLaterPeak           Reason = "optimizer_hold_for_later_peak"
	ReasonStandbyExportPV            Reason = "optimizer_standby_export_pv"
	ReasonExportHighPrice            Reason = "optimizer_export_high_price"
	ReasonDischargeBeforeCheapCharge Reason = "optimizer_discharge_before_cheap_charge"
	ReasonSelfConsumeOptimal         Reason = "optimizer_self_consume_optimal"
	ReasonSelfConsumeNoGain          Reason = "optimizer_self_consume_no_gain"
	// ReasonDischargeClampedMinSoc replaces the plan's discharge code when the
	// dispatch SoC margin (S-006a) leaves no discharge and standby is applied.
	ReasonDischargeClampedMinSoc Reason = "optimizer_discharge_clamped_min_soc"
)
