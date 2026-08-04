package relay

// nvidia_translate_ocr.go: 已迁出,本文件仅作历史索引锚点。
//
// 原 Anthropic image 块本地 OCR 降级逻辑(nvidiaImageOcrDescHeader /
// imageNotExtractablePlaceholder / ocrRecentWindowMessages /
// downgradeAnthropicImagesToText)在三层抽离重构后已迁入:
//   - ocr_downgrade_anthropic.go   协议适配层 L2(DowngradeAnthropicImagesToText)
//   - ocr_engine.go                引擎层 L1(OCRService.OcrImage)
//
// 保留本空文件:其文件名仍在 nvidia_translate.go 的拆分索引注释中被引用,且 git 历史
// 可追溯;清空而非删除避免破坏既有 import 习惯与 IDE 文件树认知。若日后清理索引注释,
// 可一并删除本文件。
