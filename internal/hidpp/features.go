package hidpp

import "fmt"

// Well-known HID++ 2.0 feature IDs. The device exposes a subset; we resolve
// each to a runtime feature *index* via IRoot before use.
const (
	FeatIRoot          = 0x0000
	FeatIFeatureSet    = 0x0001
	FeatIFeatureInfo   = 0x0002
	FeatFwInfo         = 0x0003
	FeatDeviceName     = 0x0005
	FeatBatteryStatus  = 0x1000 // BatteryUnifiedLevelStatus (older)
	FeatBatteryVoltage = 0x1001
	FeatUnifiedBattery = 0x1004
	FeatReprogControls = 0x1B04 // remappable buttons (ReprogControlsV4)
	FeatAdjustableDPI  = 0x2201
	FeatExtAdjDPI      = 0x2202
	FeatAngleSnap      = 0x2230
	FeatReportRate     = 0x8060
	FeatExtReportRate  = 0x8061
	FeatBunnyHopping   = 0x80E0
	FeatRGBEffects     = 0x8070
	FeatRGBEffectsAlt  = 0x8071
	FeatPerKeyLighting = 0x8081
	FeatOnboardProfile = 0x8100
	FeatMouseButtons   = 0x8110
)

// featureNames gives human labels for the feature explorer. Unknown IDs are
// rendered as hex so newly-discovered features (the point of the haptics hunt)
// are still legible.
var featureNames = map[uint16]string{
	0x0000: "IRoot",
	0x0001: "IFeatureSet",
	0x0002: "IFeatureInfo",
	0x0003: "DeviceFwInfo",
	0x0005: "DeviceNameType",
	0x0020: "ConfigChange",
	0x00C2: "DFUControl",
	0x1000: "BatteryUnifiedLevelStatus",
	0x1001: "BatteryVoltage",
	0x1004: "UnifiedBattery",
	0x1500: "ForcePairing",
	0x1602: "PasswordOrAuth?",
	0x1801: "ManufacturingMode",
	0x1802: "DeviceReset",
	0x1803: "GPIOAccess",
	0x1805: "OOBState",
	0x1806: "ConfigurableDeviceProperties",
	0x1814: "ChangeHost",
	0x1817: "LightspeedPrepairing",
	0x1830: "PowerModes",
	0x1890: "RFTest",
	0x1D4B: "WirelessDeviceStatus",
	0x1E00: "EnableHiddenFeatures",
	0x19B0: "Haptic",
	0x1B04: "ReprogControlsV4",
	0x1B05: "FullKeyCustomization",
	0x1B0C: "AnalogButtons",          // actuation point, rapid trigger, CLICK HAPTICS
	0x2201: "AdjustableDPI",
	0x2202: "ExtendedAdjustableDPI",
	0x2230: "AngleSnapping",
	0x2250: "MouseTuning",
	0x2251: "WheelStats",
	0x8060: "ReportRate",
	0x8061: "ExtendedAdjustableReportRate",
	0x8070: "ColorLEDEffects",
	0x8071: "RGBEffects",
	0x8081: "PerKeyLighting",
	0x8090: "ModeStatus",
	0x80E0: "BunnyHopping",
	0x8100: "OnboardProfiles",
	0x8110: "MouseButtonSpy",
	0x9403: "FlashUpdate?",
}

// FeatureName returns a readable label for a feature id.
func FeatureName(id uint16) string {
	if n, ok := featureNames[id]; ok {
		return n
	}
	return fmt.Sprintf("Unknown(0x%04X)", id)
}

// Feature is one entry of the device's feature table.
type Feature struct {
	ID       uint16
	Index    byte
	Version  byte
	Obsolete bool
	Hidden   bool
	Engineering bool
}

func (f Feature) Name() string { return FeatureName(f.ID) }

// Ping verifies the device answers HID++ 2.0 and returns its protocol version
// as "major.minor". A round-tripped marker byte guards against stale data.
func (d *Device) Ping() (string, error) {
	const marker = 0x5A
	resp, err := d.Call(FeatIRoot, 0x01, 0x00, 0x00, marker)
	if err != nil {
		return "", err
	}
	if len(resp) < 3 || resp[2] != marker {
		return "", ErrShortRead
	}
	return fmt.Sprintf("%d.%d", resp[0], resp[1]), nil
}

// FeatureIndex resolves a feature id to its runtime index via IRoot.getFeature.
// An index of 0 means the feature is absent.
func (d *Device) FeatureIndex(id uint16) (Feature, error) {
	resp, err := d.Call(FeatIRoot, 0x00, byte(id>>8), byte(id&0xFF))
	if err != nil {
		return Feature{}, err
	}
	if len(resp) < 2 {
		return Feature{}, ErrShortRead
	}
	flags := resp[1]
	return Feature{
		ID:          id,
		Index:       resp[0],
		Obsolete:    flags&0x80 != 0,
		Hidden:      flags&0x40 != 0,
		Engineering: flags&0x20 != 0,
	}, nil
}

// Has reports whether the device exposes a feature (index != 0).
func (d *Device) Has(id uint16) bool {
	f, err := d.FeatureIndex(id)
	return err == nil && f.Index != 0
}

// EnumerateFeatures walks the full feature table via IFeatureSet.
func (d *Device) EnumerateFeatures() ([]Feature, error) {
	fs, err := d.FeatureIndex(FeatIFeatureSet)
	if err != nil || fs.Index == 0 {
		return nil, fmt.Errorf("IFeatureSet unavailable: %v", err)
	}
	// getCount
	resp, err := d.Call(fs.Index, 0x00)
	if err != nil {
		return nil, err
	}
	if len(resp) < 1 {
		return nil, ErrShortRead
	}
	count := int(resp[0])

	out := make([]Feature, 0, count+1)
	// Index 0 is always IRoot, not listed by getFeatureID.
	out = append(out, Feature{ID: FeatIRoot, Index: 0})
	for i := 1; i <= count; i++ {
		r, err := d.Call(fs.Index, 0x01, byte(i)) // getFeatureID(index)
		if err != nil {
			return out, err
		}
		if len(r) < 3 {
			continue
		}
		id := uint16(r[0])<<8 | uint16(r[1])
		flags := r[2]
		out = append(out, Feature{
			ID:          id,
			Index:       byte(i),
			Obsolete:    flags&0x80 != 0,
			Hidden:      flags&0x40 != 0,
			Engineering: flags&0x20 != 0,
		})
	}
	return out, nil
}
