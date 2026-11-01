# Reverse-Engineering the Logitech PRO X 2 Superstrike

Notes on the HID++ 2.0 protocol and onboard-profile format for the **PRO X 2
Superstrike**, gathered by probing a real device on Linux. Shared to help anyone
else writing tools for this mouse (or adding it to Solaar/libratbag).

Everything here is spoken as raw HID++ reports over `/dev/hidraw` — no library.
The protocol basics (short `0x10` / long `0x11` reports, feature index +
function, software-id correlation) match standard Logitech HID++ 2.0.

> ⚠️ The single biggest gotcha: **several of this device's "get" registers
> return stale/cached values.** Do not trust read-backs to verify writes —
> verify by measuring real behaviour instead (see Report Rate / DPI below).

## Identity

- Wireless via LIGHTSPEED receiver `046d:c54d`; the paired mouse appears as a
  hid-logitech-dj child node named **`Logitech X2 SUPERSTRIKE`**, wireless PID
  **`0x40BD`**, HID++ **4.2**, device index **`0x01`** (not `0xFF`).
- Marketing name (DeviceNameType `0x0005`) reads **"PRO X2 SUPERSTRIIKE"**
  (Logitech's own typo).
- hidraw numbers are not stable — match by vendor `046d` + HID++ ping, not path.

## Feature table (36 features)

Notable ones: `0x0005` DeviceNameType, `0x1004` UnifiedBattery, `0x2202`
**ExtendedAdjustableDPI** (no classic `0x2201`), `0x8061` **ExtendedAdjustable
ReportRate** (no classic `0x8060`), `0x8100` **OnboardProfiles**, `0x1B0C`
**ANALOG_BUTTONS** (the haptics), `0x8110` MouseButtonSpy. No RGB feature
(`0x807x` absent — the mouse has no lighting). No `0x1B04` ReprogControlsV4
(so buttons are remapped via the onboard profile, not live).

## Battery — `0x1004` UnifiedBattery

`getStatus` = fn1 → `resp[0]` = state-of-charge %, `resp[2]` = charging state.

## DPI — `0x2202` ExtendedAdjustableDPI

Functions (function index in the high nibble of byte 3):
- `fn2` (0x20) getSensorDpiList — **paged**: request `[sensor=0, direction=0,
  page]`, data starts at reply byte 3, accumulate pages until a `0x0000`
  terminator. Values are 16-bit big-endian; a word with top 3 bits set
  (`val>>13 == 0b111`) is a range marker: low 13 bits = step, next word = range
  max.
- `fn5` (0x50) getSensorDpi — current DPI (big-endian) at reply bytes 1–2.
- `fn6` (0x60) setSensorDpi — `[sensor, Xhi, Xlo, Yhi, Ylo, lod]`.

**The live setter (`fn6`) is a firmware no-op on this mouse.** Verified by
measuring actual cursor counts while alternating 400/1600 DPI under steady
motion → ratio 1.0 (no change), even with on-grid values and in Host mode. DPI
is only settable via the **onboard profile** (below), which *does* apply and
persist. (`getSensorDpi` also reports a stale value, so it can't confirm a set.)

## Report rate — `0x8061` ExtendedAdjustableReportRate

- `fn1` (0x10) list → 16-bit bitmask, bit i ⇒ index i supported.
- `fn2` (0x20) get → current index. **Stale in some states** — don't trust it.
- `fn3` (0x30) set → index. Index map: `0=125, 1=250, 2=500, 3=1000, 4=2000,
  5=4000, 6=8000 Hz`.

The live `fn3` setter **does** physically change the rate in **Host mode**
(verified by measuring kernel input-report frequency: set 1000 → measured
~998 Hz) — even though `fn2` keeps reporting the old value. But it's not
persistent. The persistent rate lives in the **onboard profile** (below).

## Onboard profiles — `0x8100` OnboardProfiles

- `fn0` (0x00) getInfo → `[memoryModel(=1), profileFmt, macro, count, oob,
  buttons, sectors, size(BE16), shift]`. This mouse: 5 profiles, **sector size
  255**, 5 user buttons.
- `fn2` (0x20) getMode / `fn1` (0x10) setMode → `0x01` = onboard (hardware
  profile in control), `0x02` = host (software).
- `fn3` (0x30) setCurrentProfile `[secHi, secLo]` / `fn4` (0x40) getCurrent
  profile. Switching profiles **reloads** their settings (this is how the rate
  visibly changes between profiles).
- Memory access: `fn5` (0x50) read `[secHi, secLo, offHi, offLo]` → 16 bytes;
  `fn6` (0x60) begin-write `[secHi, secLo, 0,0, lenHi, lenLo]`; `fn7` (0x70)
  write 16 bytes; `fn8` (0x80) commit.
- The RAM control sector is `0x0000`, ROM control is `0x0100`. Reads that run
  **past** the sector are rejected, so for the odd size (255) the final 16-byte
  read must be aligned to `size-16` and its tail kept.

### Control sector (0x0000) — profile table

4-byte entries `[sectorHi, sectorLo, enabled, pad]`, terminated by `0xFFFF`.
This unit: slots → sectors `0x0001..0x0005`, `enabled` = `0x01`/`0x00`.
To enable/disable a profile, flip the `enabled` byte (offset `slot*4+2`),
recompute CRC, write the sector.

### Profile sector layouts (size 255)

Two layouts have now been observed and are detected from their contents.

#### Configured/LIGHTSPEED layout

| Offset | Field |
|---|---|
| 0 | **report rate** (see encoding below) |
| 1 | resolution index |
| 2 | shift-resolution index |
| 3 … 12 | 5 DPI words, **uint16 little-endian** |
| 13,14,15 | unverified/reserved on the Superstrike (no RGB feature exposed) |
| 16 | power mode |
| 17 | angle snapping |
| 18,19 | write count (LE) |
| **48 … 111** | **button table** — 16 × 4 bytes (see Buttons) |
| 112 … 175 | G-Shift button table |
| 160 … 207 | profile **name**, UTF-16LE |
| size-2 … size-1 | **CRC-16/CCITT** (big-endian) |

Caveats specific to this device vs Solaar's generic layout:
- **Active DPI is the *last two* DPI words** — X at slot 3 (bytes 9–10), Y at
  slot 4 (bytes 11–12). `byte[1]` (resolution index) does *not* index the live
  X/Y here. The other DPI words are disabled/placeholder slots (e.g. `0xEE00`,
  `0x2000`).
- The **button table is at offset 48**, not Solaar's generic 32.

#### Pristine/wired factory layout

A wired unit that had never been configured by G HUB used five 5-byte DPI
entries beginning at offset 4:

```
[Xlo, Xhi, Ylo, Yhi, 0x02]
```

The low `0x02` bit marks an enabled stage; clearing that bit removes the stage
from the device's Next/Previous/Cycle DPI sequence. Byte 1 selects the default
stage loaded with the profile. At least one stage must remain enabled.

The factory ladder was 800, 1200, 1600, 2400 and 3200 DPI. Its current profile
was ROM sector `0x0101`; RAM control sector `0x0000` was entirely `0xFF`, so no
editable RAM profiles existed. Factory ROM profiles may end in `0xFFFF` rather
than a stored CCITT CRC and must not be rejected solely for that reason.

On the first edit, the app clones the active ROM sector into RAM, patches only
the requested stage, writes a valid CCITT CRC, creates a minimal RAM control
table pointing at the new enabled profile, and selects that RAM sector.
Subsequent edits use the normal read/patch/CRC/write path.

The mouse's current stage is volatile state and is not exposed by the profile
sector. The desktop app therefore treats selection as a change to the stored
default: it updates byte 1, reloads the profile and writes the selected value
through Extended Adjustable DPI (`0x2202`) so the change is both immediate and
persistent. On connection the app also applies the stored default explicitly;
some firmware reports the correct resolution index while leaving the sensor at
its previous volatile value when onboard mode is re-entered. For the same
reason, normal desktop shutdown closes the HID handle without forcing the
device out of host mode; changing modes during shutdown would replace the
selected DPI with that stale stage.

Editing any stage also reloads the profile sector. The UI records the live
stage before that write and reapplies it afterward, since the reload may select
a different stage even when the edited stage was already active.

### Report-rate byte encoding (the surprising one)

`byte[0]` of the profile encodes the rate as a **power of two**:

```
Hz = 125 << byte      →  byte 0=125, 1=250, 2=500, 3=1000, 4=2000, 5=4000, 6=8000
```

(It is *not* `32000 >> byte`, which only coincidentally agrees at byte 4 =
2000 Hz.) Verified by writing each byte and **measuring** the resulting rate.
Editing it applies after the profile is (re)loaded.

### CRC

CRC-16/CCITT-FALSE: poly `0x1021`, init `0xFFFF`, no reflection, no final XOR,
computed over `sector[0 : size-2]`, stored big-endian in the last two bytes.
Write the sector with a correct CRC or the device rejects/ignores it.

### Editing strategy that works

Read the sector → patch only the bytes you change → recompute CRC → write
(`fn6`/`fn7`/`fn8`) → **read back and verify byte-for-byte**. DPI and buttons
apply live when you patch the *active* profile; report rate applies after a
profile-switch reload. Retry on CRC mismatch (transient busy reads happen).

## Haptics & analog buttons — `0x1B0C` ANALOG_BUTTONS

The Superstrike's headline feature. (Solaar names it "Analog button tuning:
actuation point, rapid trigger, haptics.")

- `fn0` getCapabilities → `[flags, buttonCount, maxAct<<2, maxRT<<2,
  maxHaptics<<2, …]`. This unit: 3 buttons reported (only **L=0, R=1** are
  user-accessible), max actuation 10, max rapid-trigger 5, max haptics 5.
- `fn2` (0x20) getConfig `[button]` → `[button, act<<2, rt<<2|sensFlag,
  haptics<<2]`.
- `fn1` (0x10) setConfig `[button, act<<2, rt<<2|sensFlag, haptics<<2]`.

Wire encoding: each value is packed in bits 7..2 (`wire = logical << 2`). The
rapid-trigger byte's **bit 0 is a firmware sensitivity flag that must be
preserved** across writes, or the device returns INVALID_ARGUMENT. These
**do** apply live (no profile reload needed).

## Scroll-wheel Bhop mode — `0x80E0` BunnyHopping

The device advertises this feature, but its payload layout is not decoded yet.
G HUB describes it as a scroll-wheel filter: the first wheel event is ignored
unless a second event occurs within a configurable 100–1000 ms window.

Run `superstrike -bhop-probe` to collect conservative diagnostic output. The
probe resolves the runtime feature index, calls probable read functions `fn0`
and `fn2` twice, and prints the raw response bytes. It intentionally does not
call probable setter `fn1`. Do not add a setter until its payload has been
verified against device responses or a G HUB USB capture.

## Buttons (via the profile, offset 48)

16 slots × 4 bytes. `byte0` high nibble = behavior:

- `0x8` SEND — `byte1` = type:
  - `0x01` BUTTON → `byte2..3` mouse mask (`Left=0x0001, Right=0x0002,
    Middle=0x0004, Back=0x0008, Forward=0x0010, DPI=0x2000`)
  - `0x02` MODIFIER_AND_KEY → `byte2` modifier mask, `byte3` USB HID keycode
  - `0x03` CONSUMER_KEY → `byte2..3` HID consumer code
  - `0x00` NO_ACTION → `byte2..3 = FFFF`
- `0x9` FUNCTION — `byte1` = function id (`NEXT_DPI=3, CYCLE_DPI=5,
  NEXT_PROFILE=8, CYCLE_PROFILE=0xA, G_SHIFT=0xB, …`), `byte2=FF`, `byte3=data`
- `0xFFFFFFFF` = unused/default.

Factory order matches the physical buttons: slot 0 Left, 1 Right, 2 Middle,
3 Back, 4 Forward.

## Measuring real behaviour (since getters lie)

- **Report rate:** open a second read-only handle to the same hidraw node (it
  broadcasts input reports to all readers) and time the gaps between non-HID++
  input reports — rate = 1 / median(interval). Or count `EV_SYN` on the evdev
  node. Move the mouse during measurement.
- **DPI:** sum `|REL_X|+|REL_Y|` from the evdev node over the same physical
  movement at two DPI settings; the ratio reveals whether DPI changed.

## Credits

Cross-referenced with [Solaar](https://github.com/pwr-Solaar/Solaar) and
[libratbag](https://github.com/libratbag/libratbag) for the general HID++ 2.0
feature/profile semantics; the Superstrike-specific deviations (rate encoding,
button offset 48, X/Y slot positions, dead live-DPI setter, stale getters) were
determined empirically and are documented above.
