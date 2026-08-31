package types

// BackgroundTerminal is one live provider terminal returned by
// thread/backgroundTerminals/list.
type BackgroundTerminal struct {
	Command    string   `json:"command"`
	Cwd        string   `json:"cwd"`
	ItemID     string   `json:"itemId"`
	ProcessID  string   `json:"processId"`
	OSPID      *uint32  `json:"osPid,omitempty"`
	CPUPercent *float64 `json:"cpuPercent,omitempty"`
	RSSKB      *uint64  `json:"rssKb,omitempty"`
}
