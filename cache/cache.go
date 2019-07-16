// # 缓存（Cache）
// 在计算机硬件中普遍存在。比如在 CPU 中就有一级缓存，二级缓存，甚至三级缓存。
// 缓存的工作原理一般是 CPU 需要读取数据时，会首先从缓存中查找需要的数据，如果找到了就直接进行处理，如果没有找到则从内存中读取数据。
// 由于 CPU 中的缓存工作速度比内存还要快，所以缓存的使用能加快 CPU 处理速度。缓存不仅仅存在于硬件中，在各种软件系统中也处处可见。
// 比如在 Web 系统中，缓存存在于服务器端，客户端或者代理服务器中。广泛使用的 CDN 网络，也可以看作一个巨大的缓存系统。
// 缓存在 Web 系统中的使用有很多好处，不仅可以减少网络流量，降低客户访问延迟，还可以减轻服务器负载。

// 目前已经存在很多高性能的缓存系统，比如 Memcache，Redis 等，尤其是 Redis，现已经广泛用于各种 Web 服务中。
// 既然有了这些功能完善的缓存系统，那为什么我们还需要自己实现一个缓存系统呢？这么做主要有两个原因，第一，通过动手实现我们可以了解缓存系统的工作原理，这也是老掉牙的理由了。
// 第二，Redis 之类的缓存系统都是独立存在的，如果只是开发一个简单的应用还需要单独使用 Redis 服务器，难免会过于复杂。
// 这时候如果有一个功能完善的软件包实现了这些功能，只需要引入这个软件包就能实现缓存功能，而不需要单独使用 Redis 服务器，就最好不过了。

// # 缓存系统的设计
// 缓存系统中，缓存的数据一般都存储在内存中，所以我们设计的缓存系统要以某一种方式管理内存中的数据。
// 既然缓存数据是存储在内存中的，那如果系统停机，那数据岂不就丢失了吗？其实一般情况下，缓存系统还支持将内存中的数据写入到文件中，在系统再次启动时，再将文件中的数据加载到内存中，这样一来就算系统停机，缓存数据也不会丢失。
// 同时缓存系统还提供过期数据清理机制，也就是说缓存的数据项是有生存时间的，如果数据项过期，则数据项会从内存中被删除，这样一来热数据会一直存在，而冷数据则会被删除，也没有必要进行缓存。
// 缓存系统还需要对外提供操作的接口，这样系统的其他组件才能使用缓存。一般情况下，缓存系统需要支持 CRUD 操作，也就是创建（添加），读取，更新和删除操作。
// 通过以上分析，可以总结出缓存系统需要有以下功能：
// 缓存数据的存储
// 过期数据项管理
// 内存数据导出，导入
// 提供 CRUD 接口

package cache

import (
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Item 缓存数据项结构
type Item struct {
	Object     interface{} // 真正的数据项
	Expiration int64       // 生存时间
}

// Expired 判断数据项是否已经过期
func (item Item) Expired() bool {
	if item.Expiration == 0 {
		return false
	}
	return time.Now().UnixNano() > item.Expiration
}

const (
	// NoExpiration 没有过期时间标志
	NoExpiration time.Duration = -1

	// DefaultExpiration 默认的过期时间
	DefaultExpiration time.Duration = 0
)

// Cache 缓存系统结构
type Cache struct {
	defaultExpiration time.Duration
	items             map[string]Item // 缓存数据项存储在 map 中
	mu                sync.RWMutex    // 读写锁
	gcInterval        time.Duration   // 过期数据项清理周期
	stopGc            chan bool
}

// 过期缓存数据项清理
func (c *Cache) gcLoop() {
	ticker := time.NewTicker(c.gcInterval)
	for {
		select {
		case <-ticker.C:
			c.DeleteExpired()
		case <-c.stopGc:
			ticker.Stop()
			return
		}
	}
}

// 删除缓存数据项
func (c *Cache) delete(k string) {
	delete(c.items, k)
}

// DeleteExpired 删除过期数据项
func (c *Cache) DeleteExpired() {
	now := time.Now().UnixNano()
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range c.items {
		if v.Expiration > 0 && now > v.Expiration {
			c.delete(k)
		}
	}
}

// Set 设置缓存数据项，如果数据项存在则覆盖
func (c *Cache) Set(k string, v interface{}, d time.Duration) {
	var e int64
	if d == DefaultExpiration {
		d = c.defaultExpiration
	}
	if d > 0 {
		e = time.Now().Add(d).UnixNano()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[k] = Item{
		Object:     v,
		Expiration: e,
	}
}

// 设置数据项, 没有锁操作
func (c *Cache) set(k string, v interface{}, d time.Duration) {
	var e int64
	if d == DefaultExpiration {
		d = c.defaultExpiration
	}
	if d > 0 {
		e = time.Now().Add(d).UnixNano()
	}
	c.items[k] = Item{
		Object:     v,
		Expiration: e,
	}
}

// 获取数据项，如果找到数据项，还需要判断数据项是否已经过期
func (c *Cache) get(k string) (interface{}, bool) {
	item, found := c.items[k]
	if !found {
		return nil, false
	}
	if item.Expired() {
		return nil, false
	}
	return item.Object, true
}

// Add 添加数据项，如果数据项已经存在，则返回错误
func (c *Cache) Add(k string, v interface{}, d time.Duration) error {
	c.mu.Lock()
	_, found := c.get(k)
	if found {
		c.mu.Unlock()
		return fmt.Errorf("Item %s already exists", k)
	}
	c.set(k, v, d)
	c.mu.Unlock()
	return nil
}

// Get 获取数据项
func (c *Cache) Get(k string) (interface{}, bool) {
	c.mu.RLock()
	item, found := c.items[k]
	if !found {
		c.mu.RUnlock()
		return nil, false
	}
	if item.Expired() {
		return nil, false
	}
	c.mu.RUnlock()
	return item.Object, true
}

// Replace 替换一个存在的数据项
func (c *Cache) Replace(k string, v interface{}, d time.Duration) error {
	c.mu.Lock()
	_, found := c.get(k)
	if !found {
		c.mu.Unlock()
		return fmt.Errorf("Item %s doesn't exist", k)
	}
	c.set(k, v, d)
	c.mu.Unlock()
	return nil
}

// Delete 删除一个数据项
func (c *Cache) Delete(k string) {
	c.mu.Lock()
	c.delete(k)
	c.mu.Unlock()
}

// Save 将缓存数据项写入到 io.Writer 中
func (c *Cache) Save(w io.Writer) (err error) {
	enc := gob.NewEncoder(w)
	defer func() {
		if x := recover(); x != nil {
			err = fmt.Errorf("Error registering item types with Gob library")
		}
	}()
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, v := range c.items {
		gob.Register(v.Object)
	}
	err = enc.Encode(&c.items)
	return
}

// SaveToFile 保存数据项到文件中
func (c *Cache) SaveToFile(file string) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	if err = c.Save(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Load 从 io.Reader 中读取数据项
func (c *Cache) Load(r io.Reader) error {
	dec := gob.NewDecoder(r)
	items := map[string]Item{}
	err := dec.Decode(&items)
	if err == nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		for k, v := range items {
			fmt.Println(k, v)
			ov, found := c.items[k]
			fmt.Println(ov, found)
			if !found || ov.Expired() {
				fmt.Println("load", k, v)
				c.items[k] = v
			}
		}
	}
	return err
}

// LoadFile 从文件中加载缓存数据项
func (c *Cache) LoadFile(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	if err = c.Load(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Count 返回缓存数据项的数量
func (c *Cache) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Flush 清空缓存
func (c *Cache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = map[string]Item{}
}

// StopGc 停止过期缓存清理
func (c *Cache) StopGc() {
	c.stopGc <- true
}

// NewCache 创建一个缓存系统
func NewCache(defaultExpiration, gcInterval time.Duration) *Cache {
	c := &Cache{
		defaultExpiration: defaultExpiration, // 默认过期时间
		gcInterval:        gcInterval,
		items:             map[string]Item{},
		stopGc:            make(chan bool),
	}
	// 开始启动过期清理 goroutine
	go c.gcLoop()
	return c
}
