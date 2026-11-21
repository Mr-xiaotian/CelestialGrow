package reporter

// Config 描述一次远程上报运行所需的最小配置。
//
// URL 为上报表服务的基础地址，末尾的 "/" 会被规范化去掉。SessionID 用于在
// 服务端标识本次运行；为空时由运行入口生成。两个周期字段以节拍为单位，0 表示
// 使用该处理器的默认周期。
type Config struct {
	URL             string // 上报服务基础地址
	SessionID       string // 上报会话标识；为空时由运行入口生成
	SnapshotPeriod  int    // 状态快照节拍周期
	InjectionPeriod int    // 注入拉取节拍周期
}
