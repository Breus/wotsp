package wotsp

import (
	"fmt"
)

// params is an internal struct that defines required parameters in WOTS. The
// parameters are derived from a Mode.
type params struct {
	w         uint
	logW      uint
	l1, l2, l int
}

// BB: this is a public Params struct used in MBPQS.
type Params struct {
	W         uint32
	LogW      uint32
	L1, L2, L uint32
}

// Mode constants specify internal parameters according to the given mode of
// operation. The available parameter sets include w = 4 and w = 16. The
// default, which is used when no explicit mode is chosen, is w = 16. This
// allows the default Mode to be selected by specifying wotsp.Mode(0).
//
// See RFC 8391 for details on the different parameter sets.
type Mode int

const (
	// W16 indicates the parameter set of W-OTS+ where w = 16. W16 is the default
	// mode.
	//
	// Passing W16 to Opts opts is equivalent to passing Mode(0), or not setting
	// the Mode at all.
	W16 Mode = iota

	// W4 indicates the parameter set of W-OTS+ where w = 4.
	W4

	// W256 indicates the parameter set of W-OTS+ where w = 256.
	W256
)

// params construct a modeParams instance based on the operating Mode, or an
// error if the mode is not valid.
func (m Mode) params() (p params) {
	switch m {
	case W4:
		p.w = 4
		p.logW = 2
		p.l1 = 128
		p.l2 = 5
	case W16:
		p.w = 16
		p.logW = 4
		p.l1 = 64
		p.l2 = 3
	case W256:
		p.w = 256
		p.logW = 8
		p.l1 = 32
		p.l2 = 2
	default:
		panic(fmt.Sprintf("invalid mode %s, must be either wotsp.W4, wotsp.W16 or wotsp.W256", m))
	}

	p.l = p.l1 + p.l2
	return
}

// String implements fmt.Stringer.
func (m Mode) String() string {
	switch m {
	case W4:
		return "W4"
	case W16:
		return "W16"
	case W256:
		return "W256"
	default:
		return fmt.Sprintf("<invalid mode %d>", m)
	}
}

// Export internal params to externally accessible Param struct.
func export(p params) (P Params) {
	return Params{
		W:    uint32(p.w),
		LogW: uint32(p.logW),
		L1:   uint32(p.l1),
		L2:   uint32(p.l2),
		L:    uint32(p.l),
	}
}

func (m *Mode) Params() (P Params) {
	return export(m.params())

}
