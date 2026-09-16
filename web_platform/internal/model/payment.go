package model

// PaymentConfig 定义管理后台与收银订单生成核心支付跳转配置
type PaymentConfig struct {
	PayProvider       string `json:"payProvider"`       // 主支付渠道: "epay" (易支付/中继) | "fake" (本地模拟沙箱)
	SiteURL           string `json:"siteUrl"`           // A站本站公网 URL
	PayReturnURL      string `json:"payReturnUrl"`      // 支付成功用户前端跳回地址 (默认 {site_url}/#/dashboard)

	// B 站合规收银切单中继 (极客工坊 Resource Hub 对齐)
	RelayURL          string `json:"relayUrl"`          // 中继下单接口 URL (如 http://127.0.0.1:8000/api/v1/relay/create)
	RelayCheckoutBase string `json:"relayCheckoutBase"` // 中继独立收银台基准 URL (如 http://crosslinkdev.online:8080)
	RelaySecret       string `json:"relaySecret"`       // 跨站通信密钥 (HMAC-SHA256 签名)
	RelayNotifyURL    string `json:"relayNotifyUrl"`    // 中继异步通知 URL (默认 {site_url}/api/v1/pay/notify/relay)

	// 易支付官方收单网关 (彩虹协议直连模式)
	EpayURL           string `json:"epayUrl"`           // 易支付网关地址 (如 https://www.ezfp.cn)
	EpayPID           string `json:"epayPid"`           // 商户编号 PID
	EpayKey           string `json:"epayKey"`           // 商户通信密钥 Key
	EpayType          string `json:"epayType"`          // 支付方式编码 (alipay / wxpay，默认 alipay)
	EpayNotifyURL     string `json:"epayNotifyUrl"`     // 易支付直连异步通知 URL (默认 {site_url}/api/v1/pay/notify/epay)
}
