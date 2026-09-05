package plot

import "sync/atomic"

// ==== Struct ====

// Counter 并发安全的种子计数器。
// 使用 atomic 操作跟踪种子总数、成功数（果实）和失败数（杂草），
// 供多个 tender 协程同时更新而无需加锁。
type Counter struct {
	seedNum  atomic.Int64
	fruitNum atomic.Int64
	weedNum  atomic.Int64

	upstreamYields map[string]*atomic.Int64
}

// ==== Constructor ====

// NewCounter 创建并返回一个新的 Counter，所有计数初始为零。
func NewCounter() *Counter {
	return &Counter{upstreamYields: make(map[string]*atomic.Int64)}
}

// ==== Adders ====

// AddSeedNum 原子地增加种子总数。
func (c *Counter) AddSeedNum(addNum int) {
	c.seedNum.Add(int64(addNum))
}

// AddFruitNum 原子地增加成功数（果实）。
func (c *Counter) AddFruitNum(addNum int) {
	c.fruitNum.Add(int64(addNum))
}

// AddWeedNum 原子地增加失败数（杂草）。
func (c *Counter) AddWeedNum(addNum int) {
	c.weedNum.Add(int64(addNum))
}

// ==== Getters ====

// GetSeedNum 返回当前 plot 的种子总数，
// 包括本地播入的种子和上游产出转入的种子。
func (c *Counter) GetSeedNum() int {
	totalSeedNum := int(c.seedNum.Load())
	for _, yieldCounter := range c.upstreamYields {
		totalSeedNum += int(yieldCounter.Load())
	}
	return totalSeedNum
}

// GetFruitNum 返回成功数（果实）。
func (c *Counter) GetFruitNum() int {
	return int(c.fruitNum.Load())
}

// GetWeedNum 返回失败数（杂草）。
func (c *Counter) GetWeedNum() int {
	return int(c.weedNum.Load())
}

// GetCompleted 返回已完成总数（果实 + 杂草）。
func (c *Counter) GetCompleted() int {
	return c.GetFruitNum() + c.GetWeedNum()
}

// ==== Predicates ====

// IsFinish 判断所有种子是否已全部完成（已完成数 == 种子总数）。
func (c *Counter) IsFinish() bool {
	return c.GetCompleted() == c.GetSeedNum()
}
