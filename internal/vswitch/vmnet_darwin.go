//go:build darwin && cgo

package vswitch

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework vmnet
#include <vmnet/vmnet.h>
#include <dispatch/dispatch.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

typedef struct {
	interface_ref iface;
	uint64_t max_packet;
	uint64_t mtu;
	char mac[32];
} nc_vmnet;

static dispatch_queue_t nc_queue(void) {
	static dispatch_queue_t q;
	static dispatch_once_t once;
	dispatch_once(&once, ^{ q = dispatch_queue_create("io.nanocloud.vswitch", DISPATCH_QUEUE_SERIAL); });
	return q;
}

// nc_vmnet_start starts a shared-mode interface: the host, DHCP from macOS
// and NAT to the outside. Whenever packets are waiting a byte is written to
// wake_fd, which is non-blocking: a full pipe already says so. out must be C
// memory; it is written from the dispatch queue.
static vmnet_return_t nc_vmnet_start(nc_vmnet *out, int wake_fd) {
	xpc_object_t desc = xpc_dictionary_create(NULL, NULL, 0);
	xpc_dictionary_set_uint64(desc, vmnet_operation_mode_key, VMNET_SHARED_MODE);
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	__block vmnet_return_t status = VMNET_FAILURE;
	interface_ref iface = vmnet_start_interface(desc, nc_queue(), ^(vmnet_return_t st, xpc_object_t param) {
		status = st;
		if (st == VMNET_SUCCESS) {
			out->max_packet = xpc_dictionary_get_uint64(param, vmnet_max_packet_size_key);
			out->mtu = xpc_dictionary_get_uint64(param, vmnet_mtu_key);
			const char *mac = xpc_dictionary_get_string(param, vmnet_mac_address_key);
			if (mac != NULL) {
				strlcpy(out->mac, mac, sizeof out->mac);
			}
		}
		dispatch_semaphore_signal(done);
	});
	xpc_release(desc);
	if (iface == NULL) {
		dispatch_release(done);
		return VMNET_FAILURE;
	}
	if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 30 * NSEC_PER_SEC)) != 0) {
		return VMNET_SETUP_INCOMPLETE; // the handler may still run, so out and done are left alone
	}
	dispatch_release(done);
	if (status != VMNET_SUCCESS) {
		return status;
	}
	out->iface = iface;
	vmnet_interface_set_event_callback(iface, VMNET_INTERFACE_PACKETS_AVAILABLE, nc_queue(),
		^(interface_event_t ev, xpc_object_t e) {
			char b = 1;
			(void)write(wake_fd, &b, 1);
		});
	return VMNET_SUCCESS;
}

static vmnet_return_t nc_vmnet_read(interface_ref iface, void *buf, size_t len, size_t *n, int *count) {
	struct iovec iov = { .iov_base = buf, .iov_len = len };
	struct vmpktdesc pd = { .vm_pkt_size = len, .vm_pkt_iov = &iov, .vm_pkt_iovcnt = 1, .vm_flags = 0 };
	*count = 1;
	vmnet_return_t r = vmnet_read(iface, &pd, count);
	*n = pd.vm_pkt_size;
	return r;
}

static vmnet_return_t nc_vmnet_write(interface_ref iface, void *buf, size_t len) {
	struct iovec iov = { .iov_base = buf, .iov_len = len };
	struct vmpktdesc pd = { .vm_pkt_size = len, .vm_pkt_iov = &iov, .vm_pkt_iovcnt = 1, .vm_flags = 0 };
	int count = 1;
	return vmnet_write(iface, &pd, &count);
}

// nc_vmnet_stop silences the wake callback, then stops the interface and
// waits for it, so nothing writes to wake_fd once it returns.
static void nc_vmnet_stop(interface_ref iface) {
	vmnet_interface_set_event_callback(iface, VMNET_INTERFACE_PACKETS_AVAILABLE, NULL, NULL);
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	if (vmnet_stop_interface(iface, nc_queue(), ^(vmnet_return_t st) { dispatch_semaphore_signal(done); }) == VMNET_SUCCESS) {
		dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC));
	}
	// Drain the queue: a wake callback already running finishes first.
	dispatch_sync(nc_queue(), ^{});
}
*/
import "C"

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"unsafe"
)

// vmnetErrors names vmnet's status codes.
var vmnetErrors = map[C.vmnet_return_t]string{
	C.VMNET_FAILURE:              "failure",
	C.VMNET_MEM_FAILURE:          "out of memory",
	C.VMNET_INVALID_ARGUMENT:     "invalid argument",
	C.VMNET_SETUP_INCOMPLETE:     "setup did not complete",
	C.VMNET_INVALID_ACCESS:       "permission denied (the switch must run as root)",
	C.VMNET_PACKET_TOO_BIG:       "packet too big",
	C.VMNET_BUFFER_EXHAUSTED:     "buffers exhausted",
	C.VMNET_TOO_MANY_PACKETS:     "too many packets",
	C.VMNET_SHARING_SERVICE_BUSY: "another sharing service is using the network",
	C.VMNET_NOT_AUTHORIZED:       "not authorized",
}

func vmnetError(st C.vmnet_return_t) error {
	if s, ok := vmnetErrors[st]; ok {
		return fmt.Errorf("vmnet: %s", s)
	}
	return fmt.Errorf("vmnet: status %d", int(st))
}

// Vmnet is the uplink: one shared-mode vmnet interface.
type Vmnet struct {
	info  *C.nc_vmnet
	wake  *os.File // read end; a byte means packets are waiting
	wakeW *os.File

	MAC      string
	MTU      int
	MaxFrame int

	// life is held shared around every use of the interface and exclusively
	// to stop it, so nothing touches it once stopped. wmu serialises writes.
	life   sync.RWMutex
	wmu    sync.Mutex
	closed bool
	once   sync.Once
}

// StartVmnet starts the uplink. It needs root.
func StartVmnet() (*Vmnet, error) {
	wake, wakeW, err := os.Pipe() // both ends non-blocking
	if err != nil {
		return nil, err
	}
	var wfd uintptr
	raw, err := wakeW.SyscallConn()
	if err == nil {
		err = raw.Control(func(fd uintptr) { wfd = fd }) // not Fd(), which would make it blocking
	}
	if err != nil {
		wake.Close()  //nolint:errcheck // failing
		wakeW.Close() //nolint:errcheck // failing
		return nil, err
	}
	info := (*C.nc_vmnet)(C.calloc(1, C.sizeof_nc_vmnet))
	if st := C.nc_vmnet_start(info, C.int(wfd)); st != C.VMNET_SUCCESS {
		if st != C.VMNET_SETUP_INCOMPLETE { // otherwise the handler may yet write to info
			C.free(unsafe.Pointer(info))
		}
		wake.Close()  //nolint:errcheck // failing
		wakeW.Close() //nolint:errcheck // failing
		return nil, vmnetError(st)
	}
	return &Vmnet{
		info: info, wake: wake, wakeW: wakeW,
		MAC: C.GoString(&info.mac[0]), MTU: int(info.mtu), MaxFrame: int(info.max_packet),
	}, nil
}

// ReadFrame returns the next frame, waiting for one if none is queued.
func (v *Vmnet) ReadFrame(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, errors.New("vmnet: empty buffer")
	}
	wakeBuf := make([]byte, 64)
	for {
		n, got, err := v.read(b)
		if err != nil || got {
			return n, err
		}
		// Nothing queued: sleep until vmnet signals. Draining several wake-ups
		// at once is fine because read empties the queue.
		if _, err := v.wake.Read(wakeBuf); err != nil {
			return 0, net.ErrClosed
		}
	}
}

func (v *Vmnet) read(b []byte) (int, bool, error) {
	v.life.RLock()
	defer v.life.RUnlock()
	if v.closed {
		return 0, false, net.ErrClosed
	}
	var n C.size_t
	var count C.int
	if st := C.nc_vmnet_read(v.info.iface, unsafe.Pointer(&b[0]), C.size_t(len(b)), &n, &count); st != C.VMNET_SUCCESS {
		return 0, false, vmnetError(st)
	}
	return int(n), count > 0, nil
}

// WriteFrame hands one frame to the host. vmnet copies it before returning.
func (v *Vmnet) WriteFrame(b []byte) error {
	if len(b) == 0 || len(b) > v.MaxFrame {
		return fmt.Errorf("vmnet: frame of %d bytes", len(b))
	}
	v.life.RLock()
	defer v.life.RUnlock()
	if v.closed {
		return net.ErrClosed
	}
	v.wmu.Lock()
	st := C.nc_vmnet_write(v.info.iface, unsafe.Pointer(&b[0]), C.size_t(len(b)))
	v.wmu.Unlock()
	switch st {
	case C.VMNET_SUCCESS:
		return nil
	case C.VMNET_BUFFER_EXHAUSTED:
		return ErrBusy
	default:
		return vmnetError(st)
	}
}

// Close stops the interface. A ReadFrame waiting for packets returns.
func (v *Vmnet) Close() error {
	v.once.Do(func() {
		v.life.Lock()
		v.closed = true
		C.nc_vmnet_stop(v.info.iface)
		v.life.Unlock()
		v.wakeW.Close() //nolint:errcheck // wakes the reader with EOF
		v.wake.Close()  //nolint:errcheck // done
		C.free(unsafe.Pointer(v.info))
	})
	return nil
}
