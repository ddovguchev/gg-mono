// Package fonts встраивает шрифты приложения.
// gofont (Go Regular) не содержит кириллицу и эмодзи, поэтому для
// русских субтитров добавляется Noto Sans (лицензия OFL).
package fonts

import "embed"

//go:embed NotoSans-Regular.ttf
var FS embed.FS

const NotoFileName = "NotoSans-Regular.ttf"
