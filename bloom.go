package main

// Ten bits per key and seven probes give approximately 0.8% false positives.
// Both live records and tombstones must be represented to prevent resurrection.
type bloomFilter struct{ words []uint64 }

func newBloom(count int) *bloomFilter {
	bits := count * 10
	if bits < 64 {
		bits = 64
	}
	return &bloomFilter{words: make([]uint64, (bits+63)/64)}
}
func mix(h uint64) uint64 {
	h ^= h >> 30
	h *= 0xbf58476d1ce4e5b9
	h ^= h >> 27
	h *= 0x94d049bb133111eb
	return h ^ (h >> 31)
}
func (b *bloomFilter) addHash(h uint64) {
	a, d := mix(h), mix(h^0x9e3779b97f4a7c15)|1
	n := uint64(len(b.words) * 64)
	for i := uint64(0); i < 7; i++ {
		bit := (a + i*d) % n
		b.words[bit/64] |= uint64(1) << (bit % 64)
	}
}
func (b *bloomFilter) contains(key string) bool {
	if b == nil {
		return true
	}
	h := hash(key)
	a, d := mix(h), mix(h^0x9e3779b97f4a7c15)|1
	n := uint64(len(b.words) * 64)
	for i := uint64(0); i < 7; i++ {
		bit := (a + i*d) % n
		if b.words[bit/64]&(uint64(1)<<(bit%64)) == 0 {
			return false
		}
	}
	return true
}
