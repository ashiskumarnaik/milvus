package cache

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/assert"
)

func TestLRUCache(t *testing.T) {
	cacheBuilder := NewCacheBuilder[int, int]().WithLoader(func(key int) (int, bool) {
		return key, true
	})

	t.Run("test loader", func(t *testing.T) {
		size := 10
		cache := cacheBuilder.WithCapacity(int64(size)).Build()

		for i := 0; i < size; i++ {
			err := cache.Do(i, func(v int) error {
				assert.Equal(t, i, v)
				return nil
			})
			assert.NoError(t, err)
		}
	})

	t.Run("test finalizer", func(t *testing.T) {
		size := 10
		finalizeSeq := make([]int, 0)
		cache := cacheBuilder.WithCapacity(int64(size)).WithFinalizer(func(key, value int) error {
			finalizeSeq = append(finalizeSeq, key)
			return nil
		}).Build()

		for i := 0; i < size*2; i++ {
			err := cache.Do(i, func(v int) error {
				assert.Equal(t, i, v)
				return nil
			})
			assert.NoError(t, err)
		}
		assert.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, finalizeSeq)

		// Hit the cache again, there should be no swap-out
		for i := size; i < size*2; i++ {
			err := cache.Do(i, func(v int) error {
				assert.Equal(t, i, v)
				return nil
			})
			assert.NoError(t, err)
		}
		assert.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, finalizeSeq)
	})

	t.Run("test scavenger", func(t *testing.T) {
		finalizeSeq := make([]int, 0)
		sumCapacity := 20 // inserting 1 to 19, capacity is set to sum of 20, expecting (19) at last.
		cache := cacheBuilder.WithLazyScavenger(func(key int) int64 {
			return int64(key)
		}, int64(sumCapacity)).WithFinalizer(func(key, value int) error {
			finalizeSeq = append(finalizeSeq, key)
			return nil
		}).Build()

		for i := 0; i < 20; i++ {
			err := cache.Do(i, func(v int) error {
				assert.Equal(t, i, v)
				return nil
			})
			assert.NoError(t, err)
		}
		assert.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18}, finalizeSeq)
	})

	t.Run("test do negative", func(t *testing.T) {
		cache := cacheBuilder.Build()
		theErr := errors.New("error")
		err := cache.Do(-1, func(v int) error {
			return theErr
		})
		assert.Equal(t, theErr, err)
	})

	t.Run("test scavenge negative", func(t *testing.T) {
		finalizeSeq := make([]int, 0)
		sumCapacity := 20 // inserting 1 to 19, capacity is set to sum of 20, expecting (19) at last.
		cache := cacheBuilder.WithLazyScavenger(func(key int) int64 {
			return int64(key)
		}, int64(sumCapacity)).WithFinalizer(func(key, value int) error {
			finalizeSeq = append(finalizeSeq, key)
			return nil
		}).Build()

		for i := 0; i < 20; i++ {
			err := cache.Do(i, func(v int) error {
				assert.Equal(t, i, v)
				return nil
			})
			assert.NoError(t, err)
		}
		assert.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18}, finalizeSeq)
		err := cache.Do(100, func(v int) error {
			return nil
		})
		assert.Equal(t, ErrNotEnoughSpace, err)
	})

	t.Run("test load negative", func(t *testing.T) {
		cache := NewCacheBuilder[int, int]().WithLoader(func(key int) (int, bool) {
			if key < 0 {
				return 0, false
			}
			return key, true
		}).Build()
		err := cache.Do(0, func(v int) error {
			return nil
		})
		assert.NoError(t, err)
		err = cache.Do(-1, func(v int) error {
			return nil
		})
		assert.Equal(t, ErrNoSuchItem, err)
	})
}

func TestLRUCacheConcurrency(t *testing.T) {
	t.Run("test race condition", func(t *testing.T) {
		numEvict := new(atomic.Int32)
		cache := NewCacheBuilder[int, int]().WithLoader(func(key int) (int, bool) {
			return key, true
		}).WithCapacity(10).WithFinalizer(func(key, value int) error {
			numEvict.Add(1)
			return nil
		}).Build()

		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					err := cache.Do(j, func(v int) error {
						return nil
					})
					assert.NoError(t, err)
				}
			}(i)
		}
		wg.Wait()
	})

	t.Run("test not enough space", func(t *testing.T) {
		cache := NewCacheBuilder[int, int]().WithLoader(func(key int) (int, bool) {
			return key, true
		}).WithCapacity(1).WithFinalizer(func(key, value int) error {
			return nil
		}).Build()

		var wg sync.WaitGroup  // Let key 1000 be blocked
		var wg1 sync.WaitGroup // Make sure goroutine is started
		wg.Add(1)
		wg1.Add(1)
		go cache.Do(1000, func(v int) error {
			wg1.Done()
			wg.Wait()
			return nil
		})
		wg1.Wait()
		err := cache.Do(1001, func(v int) error {
			return nil
		})
		wg.Done()
		assert.Equal(t, ErrNotEnoughSpace, err)
	})
}
