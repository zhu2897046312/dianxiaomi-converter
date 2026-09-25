package main

import (
	"context"
	"net/http"
	"strings"

	"dianxiaomi-converter/internal/imagefilter"
	"dianxiaomi-converter/internal/model"
)

// visitConfiguredImages also covers values that defaults or SKU overrides could
// reintroduce after the model has been cleaned.
func visitConfiguredImages(cfg *Config, visit func(string, bool) string) {
	dxm := func(fields map[string]string) {
		for k, v := range fields {
			switch k {
			case "预览图", "*轮播图", "*产品素材图", "外包装图片":
				fields[k] = visit(v, false)
			case "产品描述":
				fields[k] = visit(v, true)
			}
		}
	}
	dxm(cfg.Defaults)
	dxm(cfg.Dianxiaomi.Defaults)
	for _, fields := range cfg.Overrides {
		dxm(fields)
	}
	for _, fields := range cfg.Dianxiaomi.Overrides {
		dxm(fields)
	}
	for k, v := range cfg.Medusa.Defaults {
		if k == "Product Thumbnail" || (strings.HasPrefix(k, "Product Image ") && strings.HasSuffix(k, " Url")) {
			cfg.Medusa.Defaults[k] = visit(v, false)
		}
		if k == "Product Description" {
			cfg.Medusa.Defaults[k] = visit(v, true)
		}
	}
}

func filterProducts(ctx context.Context, products []model.Product, cfg *Config, client *http.Client, progress func(int, int, imagefilter.Check)) (*imagefilter.Report, error) {
	var urls []string
	collect := func(s string, isHTML bool) string {
		if isHTML {
			urls = append(urls, imagefilter.HTMLURLs(s)...)
		} else {
			urls = append(urls, imagefilter.List(s)...)
		}
		return s
	}
	for _, p := range products {
		for _, im := range p.Images {
			collect(im.URL, false)
		}
		for _, v := range p.Variants {
			collect(v.Image, false)
		}
		collect(p.Description, true)
	}
	visitConfiguredImages(cfg, collect)
	policy, err := imagefilter.CheckURLs(ctx, urls, cfg.ImageFilter, client, progress)
	if err != nil {
		return nil, err
	}
	// Keep an original candidate outside the shared model. Only Dianxiaomi may
	// restore this one URL when all product/variant/config images are gone.
	blocked := map[string]bool{}
	for _, check := range policy.Report.Results {
		if check.Removed {
			blocked[check.URL] = true
		}
	}
	cfg.Dianxiaomi.FallbackImages = map[string]string{}
	cfg.Dianxiaomi.SourceDescriptions = map[string]string{}
	for _, p := range products {
		cfg.Dianxiaomi.SourceDescriptions[p.Handle] = policy.HTMLForDianxiaomi(p.Description)
		var candidates []string
		for _, im := range p.Images {
			candidates = append(candidates, imagefilter.List(im.URL)...)
		}
		for _, v := range p.Variants {
			candidates = append(candidates, imagefilter.List(v.Image)...)
		}
		for _, u := range candidates {
			if blocked[u] {
				cfg.Dianxiaomi.FallbackImages[p.Handle] = u
				break
			}
		}
	}
	for i := range products {
		p := &products[i]
		var images []model.ProductImage
		seen := map[string]bool{}
		for _, im := range p.Images {
			im.URL = policy.List(im.URL)
			if im.URL != "" && !seen[im.URL] {
				seen[im.URL] = true
				images = append(images, im)
			}
		}
		p.Images = images
		for j := range p.Variants {
			p.Variants[j].Image = policy.List(p.Variants[j].Image)
		}
		p.Description = policy.HTML(p.Description)
	}
	visitConfiguredImages(cfg, func(s string, isHTML bool) string {
		if isHTML {
			return policy.HTML(s)
		}
		return policy.List(s)
	})
	return &policy.Report, nil
}
