package downloader

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"

	_ "golang.org/x/image/webp"
)

func decodeImage(data []byte, maxBytes int) (image.Image, string, error) {
	if len(data) == 0 || len(data) > maxBytes {
		return nil, "", fmt.Errorf("图片为空或超过大小限制")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("不是支持的图片（JPEG/PNG/GIF/WebP）: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
		return nil, "", fmt.Errorf("图片尺寸异常或超过 4000 万像素")
	}
	im, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("图片不完整: %w", err)
	}
	if format == "jpeg" {
		format = "jpg"
	}
	return im, format, nil
}

func PrepareImage(data []byte, cfg Config) ([]byte, string, string, error) {
	if err := cfg.Validate(); err != nil {
		return nil, "", "", err
	}
	limit := cfg.MaxImageMB * 1024 * 1024
	im, source, err := decodeImage(data, limit)
	if err != nil {
		return nil, "", "", err
	}
	target := cfg.OutputExtension()
	if target == "original" {
		return data, source, source, nil
	}
	if target == "png" && source == "png" {
		return data, source, target, nil
	}
	out := &limitedImageBuffer{limit: limit}
	switch target {
	case "png":
		err = png.Encode(out, im)
	case "jpg":
		opaque := image.NewRGBA(im.Bounds())
		draw.Draw(opaque, opaque.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.Draw(opaque, opaque.Bounds(), im, im.Bounds().Min, draw.Over)
		err = jpeg.Encode(out, opaque, &jpeg.Options{Quality: cfg.JPEGQuality})
	}
	if err != nil {
		return nil, source, target, fmt.Errorf("转换为 %s 失败: %w", target, err)
	}
	return out.Bytes(), source, target, nil
}

type limitedImageBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedImageBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, fmt.Errorf("转换后的图片超过大小限制")
	}
	return b.Buffer.Write(p)
}
