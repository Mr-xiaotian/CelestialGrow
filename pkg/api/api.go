package api

import (
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/observer"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

// Farm 是对外暴露的农场类型。
type Farm = farm.Farm

// Plot 是对外暴露的泛型节点类型。
type Plot[S any, F any] = plot.Plot[S, F]

// PlotNode 是 Farm 连接 plot 时使用的统一接口。
type PlotNode = plot.PlotNode

// Option 是 Plot 的可选配置。
type Option = plot.Option

// NewFarm 创建一个 Farm。
func NewFarm(name string, logLevel string) *Farm {
	return farm.NewFarm(name, logLevel)
}

// NewPlot 创建一个 Plot。
func NewPlot[S any, F any](name string, cultivator func(S) (F, error), opts ...Option) *Plot[S, F] {
	return plot.NewPlot(name, cultivator, opts...)
}

// NewProgressBar 创建一个进度条。
func NewProgressBar(description string) *observer.ProgressBar {
	return observer.NewProgressBar(description)
}

var (
	WithTenders    = plot.WithTenders
	WithChanSize   = plot.WithChanSize
	WithMaxRetries = plot.WithMaxRetries
	WithRetryDelay = func(fn func(int) time.Duration) Option {
		return plot.WithRetryDelay(fn)
	}
	WithRetryIf  = plot.WithRetryIf
	WithLogLevel = plot.WithLogLevel
)
