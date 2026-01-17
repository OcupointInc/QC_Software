package shm_ring

import (
	"fmt"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

// RingHeader sits at the very beginning of the shared memory
type RingHeader struct {
	Magic    uint64 // For validation
	Size     uint64 // Total data size (excluding header)
	Head     uint64 // Writer position (byte offset)
	Tail     uint64 // Reader position (byte offset)
	Version  uint32
	Channels uint32
}

const (
	HeaderSize = uint64(unsafe.Sizeof(RingHeader{}))
	MagicValue = 0x5143415054555245 // "QCAPTURE"
)

type ShmRing struct {
	fd     int
	data   []byte
	header *RingHeader
	total  uint64
}

// Create creates a new shared memory ring buffer
func Create(name string, size uint64) (*ShmRing, error) {
	// 1. Open SHM using unix.SYS_SHM_OPEN or similar? 
	// Actually, on Linux, SHM is just files in /dev/shm.
	// Using os.OpenFile on /dev/shm/name is equivalent and safer.
	path := "/dev/shm" + name
	
	f, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL, 0666)
	if err != nil {
		if err == unix.EEXIST {
			return Open(name)
		}
		return nil, fmt.Errorf("open shm: %w", err)
	}

	totalSize := HeaderSize + size

	// 2. Truncate to size
	if err := unix.Ftruncate(f, int64(totalSize)); err != nil {
		unix.Close(f)
		return nil, fmt.Errorf("ftruncate: %w", err)
	}

	// 3. Mmap
	data, err := unix.Mmap(f, 0, int(totalSize), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		unix.Close(f)
		return nil, fmt.Errorf("mmap: %w", err)
	}

	ring := &ShmRing{
		fd:    f,
		data:  data,
		total: size,
	}

	// Initialize header
	ring.header = (*RingHeader)(unsafe.Pointer(&data[0]))
	ring.header.Magic = MagicValue
	ring.header.Size = size
	ring.header.Version = 1
	atomic.StoreUint64(&ring.header.Head, 0)
	atomic.StoreUint64(&ring.header.Tail, 0)

	return ring, nil
}

// Open opens an existing shared memory ring buffer
func Open(name string) (*ShmRing, error) {
	path := "/dev/shm" + name
	f, err := unix.Open(path, unix.O_RDWR, 0666)
	if err != nil {
		return nil, fmt.Errorf("open shm: %w", err)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(f, &stat); err != nil {
		unix.Close(f)
		return nil, fmt.Errorf("fstat: %w", err)
	}

	data, err := unix.Mmap(f, 0, int(stat.Size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		unix.Close(f)
		return nil, fmt.Errorf("mmap: %w", err)
	}

	ring := &ShmRing{
		fd:    f,
		data:  data,
		total: uint64(stat.Size) - HeaderSize,
	}

	ring.header = (*RingHeader)(unsafe.Pointer(&data[0]))
	if ring.header.Magic != MagicValue {
		ring.Close()
		return nil, fmt.Errorf("invalid magic value in shm")
	}

	return ring, nil
}

// Write writes data into the ring buffer at the Head
func (r *ShmRing) Write(p []byte) (n int, err error) {
	n = len(p)
	if uint64(n) > r.total {
		return 0, fmt.Errorf("write larger than ring size")
	}

	head := atomic.LoadUint64(&r.header.Head)
	dest := r.data[HeaderSize:]
	
	firstPart := r.total - head
	if uint64(n) <= firstPart {
		copy(dest[head:], p)
	} else {
		copy(dest[head:], p[:firstPart])
		copy(dest[0:], p[firstPart:])
	}

	newHead := (head + uint64(n)) % r.total
	atomic.StoreUint64(&r.header.Head, newHead)
	
	return n, nil
}

func (r *ShmRing) GetPointers() (uint64, uint64) {
	return atomic.LoadUint64(&r.header.Head), atomic.LoadUint64(&r.header.Tail)
}

func (r *ShmRing) SetTail(tail uint64) {
	atomic.StoreUint64(&r.header.Tail, tail % r.total)
}

func (r *ShmRing) Data() []byte {
	return r.data[HeaderSize:]
}

func (r *ShmRing) Close() error {
	if r.data != nil {
		unix.Munmap(r.data)
		r.data = nil
	}
	if r.fd != 0 {
		unix.Close(r.fd)
		r.fd = 0
	}
	return nil
}

func Remove(name string) error {
	path := "/dev/shm" + name
	err := unix.Unlink(path)
	if err != nil && err != unix.ENOENT {
		return err
	}
	return nil
}