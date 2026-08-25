package grow

import "sync/atomic"

// ==== Struct ====

// Counter 并发安全的种子计数器。
// 使用 atomic 操作跟踪种子总数、成功数（果实）和失败数（杂草），
// 供多个 tend 协程同时更新而无需加锁。
type Counter struct {
	seedNum  atomic.Int64
	fruitNum atomic.Int64
	weedNum  atomic.Int64
}

// ==== Constructor ====

// NewCounter 创建并返回一个新的 Counter，所有计数初始为零。
func NewCounter() *Counter {
	return &Counter{}
}

// ==== Setters ====

// SetSeedNum 原子地设置种子总数。
func (c *Counter) SetSeedNum(seedNum int) {
	c.seedNum.Store(int64(seedNum))
}

// ==== Adders ====

// AddSeedNum 原子地增加种子总数。
func (c *Counter) AddSeedNum(addNNum int) {
	c.seedNum.Add(int64(addNNum))
}

// AddFruitNum 原子地增加成功数（果实）。
func (c *Counter) AddFruitNum(addNNum int) {
	c.fruitNum.Add(int64(addNNum))
}

// AddWeedNum 原子地增加失败数（杂草）。
func (c *Counter) AddWeedNum(addNNum int) {
	c.weedNum.Add(int64(addNNum))
}

// ==== Getters ====

// GetSeedNum 返回种子总数。
func (c *Counter) GetSeedNum() int {
	return int(c.seedNum.Load())
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
