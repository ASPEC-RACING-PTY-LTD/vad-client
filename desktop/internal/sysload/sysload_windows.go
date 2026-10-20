//go:build windows

package sysload

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                    = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes          = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx    = kernel32.NewProc("GlobalMemoryStatusEx")
	shell32                     = windows.NewLazySystemDLL("shell32.dll")
	procSHQueryUserNotification = shell32.NewProc("SHQueryUserNotificationState")
	pdh                         = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQuery            = pdh.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounter    = pdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData     = pdh.NewProc("PdhCollectQueryData")
	procPdhGetFormattedArray    = pdh.NewProc("PdhGetFormattedCounterArrayW")
	procPdhCloseQuery           = pdh.NewProc("PdhCloseQuery")
)

const (
	pdhFmtDouble   = 0x00000200
	pdhFmtNoCap100 = 0x00008000
	pdhMoreData    = 0x800007D2
	// SHQueryUserNotificationState results that mean something full-screen.
	qunsBusy             = 2
	qunsD3DFullScreen    = 3
	qunsPresentationMode = 4
)

type winSampler struct {
	mu        sync.Mutex
	query     uintptr
	disk, gpu []uintptr
	idle      uint64
	total     uint64
	own       uint64
	at        time.Time
}

func newSampler() Sampler {
	s := &winSampler{}
	if procPdhOpenQuery.Find() == nil {
		var q uintptr
		if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&q))); r == 0 {
			s.query = q
			s.disk = s.add(`\PhysicalDisk(*)\% Idle Time`)
			s.gpu = append(s.add(`\GPU Engine(*engtype_3D)\Utilization Percentage`), s.add(`\GPU Engine(*engtype_Compute*)\Utilization Percentage`)...)
			procPdhCollectQueryData.Call(s.query)
		}
	}
	s.idle, s.total = systemTimes()
	s.own = processTime()
	s.at = time.Now()
	return s
}

func (s *winSampler) add(path string) []uintptr {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	var c uintptr
	if r, _, _ := procPdhAddEnglishCounter.Call(s.query, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&c))); r != 0 {
		return nil
	}
	return []uintptr{c}
}

func (s *winSampler) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.query != 0 {
		procPdhCloseQuery.Call(s.query)
		s.query = 0
	}
}

func (s *winSampler) Sample() Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Sample{CPU: -1, OwnCPU: -1, Memory: -1, Disk: -1, GPU: -1, CPUs: cpus()}
	idle, total := systemTimes()
	own := processTime()
	now := time.Now()
	if total > s.total {
		out.CPU = clamp01(1 - float64(idle-s.idle)/float64(total-s.total))
		if wall := now.Sub(s.at); wall > 0 {
			// processTime is in 100 ns units.
			out.OwnCPU = clamp01(float64(own-s.own) * 100 / float64(wall.Nanoseconds()) / float64(out.CPUs))
		}
	}
	s.idle, s.total, s.own, s.at = idle, total, own, now
	out.Memory, out.RAMBytes = memory()
	if s.query != 0 {
		if r, _, _ := procPdhCollectQueryData.Call(s.query); r == 0 {
			if v, ok := values(s.disk); ok {
				// The busiest disk counts: an idle second disk does not hide a busy one.
				busy := 0.0
				for _, idle := range v {
					busy = max(busy, 1-clamp01(idle/100))
				}
				out.Disk = busy
			}
			if v, ok := values(s.gpu); ok {
				sum := 0.0
				for _, x := range v {
					sum += x
				}
				out.GPU = clamp01(sum / 100)
			}
		}
	}
	out.FullScreen = fullScreen()
	return out
}

// values returns every instance value of the counters, without "_Total".
func values(counters []uintptr) ([]float64, bool) {
	type item struct {
		name   *uint16
		status uint32
		_      uint32
		value  float64
	}
	var out []float64
	ok := false
	for _, c := range counters {
		var size, count uint32
		r, _, _ := procPdhGetFormattedArray.Call(c, pdhFmtDouble|pdhFmtNoCap100, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
		if uint32(r) != pdhMoreData || size == 0 {
			continue
		}
		buf := make([]byte, size)
		if r, _, _ := procPdhGetFormattedArray.Call(c, pdhFmtDouble|pdhFmtNoCap100, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0]))); r != 0 || count == 0 {
			continue
		}
		for _, it := range unsafe.Slice((*item)(unsafe.Pointer(&buf[0])), count) {
			if it.status > 1 { // PDH_CSTATUS_VALID_DATA or NEW_DATA
				continue
			}
			if windows.UTF16PtrToString(it.name) == "_Total" {
				continue
			}
			out = append(out, it.value)
			ok = true
		}
	}
	return out, ok
}

func filetime(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }

func systemTimes() (idle, total uint64) {
	var i, k, u windows.Filetime
	if r, _, _ := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&i)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u))); r == 0 {
		return 0, 0
	}
	return filetime(i), filetime(k) + filetime(u) // kernel time includes idle time
}

// processTime returns this process's processor time in 100 ns units.
func processTime() uint64 {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &c, &e, &k, &u); err != nil {
		return 0
	}
	return filetime(k) + filetime(u)
}

func memory() (float64, uint64) {
	var m struct {
		Length               uint32
		MemoryLoad           uint32
		TotalPhys            uint64
		AvailPhys            uint64
		TotalPageFile        uint64
		AvailPageFile        uint64
		TotalVirtual         uint64
		AvailVirtual         uint64
		AvailExtendedVirtual uint64
	}
	m.Length = uint32(unsafe.Sizeof(m))
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 || m.TotalPhys == 0 {
		return -1, 0
	}
	return clamp01(1 - float64(m.AvailPhys)/float64(m.TotalPhys)), m.TotalPhys
}

// fullScreen reports a full-screen program in the user's session. The
// service (session 0) cannot see it; a game still shows as graphics load.
func fullScreen() bool {
	if procSHQueryUserNotification.Find() != nil {
		return false
	}
	var st int32
	if r, _, _ := procSHQueryUserNotification.Call(uintptr(unsafe.Pointer(&st))); r != 0 {
		return false
	}
	return st == qunsBusy || st == qunsD3DFullScreen || st == qunsPresentationMode
}

func clamp01(v float64) float64 { return max(0, min(1, v)) }
