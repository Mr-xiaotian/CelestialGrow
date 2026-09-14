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

// SplitPlot 是对外暴露的拆分节点类型。
type SplitPlot[S any, F any] = plot.SplitPlot[S, F]

// RoutePlot 是对外暴露的路由节点类型。
type RoutePlot[S any, Y any] = plot.RoutePlot[S, Y]

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

// NewSplitPlot 创建一个 SplitPlot。
func NewSplitPlot[S any, F any](name string, splitter func(S) ([]F, error), opts ...Option) *SplitPlot[S, F] {
	return plot.NewSplitPlot(name, splitter, opts...)
}

// NewRoutePlot 创建一个 RoutePlot。
func NewRoutePlot[S any, Y any](name string, cultivator func(S) (map[string]Y, error), opts ...Option) *RoutePlot[S, Y] {
	return plot.NewRoutePlot(name, cultivator, opts...)
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
