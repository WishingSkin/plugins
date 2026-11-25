package skinpng

import (
	"image"
	"image/color"
	"testing"
)

func fillSolidRect(img *image.NRGBA, x, y, w, h int, c color.NRGBA) {
	for py := range h {
		for px := range w {
			img.SetNRGBA(x+px, y+py, c)
		}
	}
}

func TestUpgradeLegacySkinsBothStandardAndHD(t *testing.T) {
	legacy := image.NewNRGBA(image.Rect(0, 0, 64, 32))
	// Paint distinct left edge vs right edge of Right Leg Front (4..7, 20..31)
	fillSolidRect(legacy, 4, 20, 1, 12, color.NRGBA{255, 0, 0, 255})
	fillSolidRect(legacy, 7, 20, 1, 12, color.NRGBA{0, 0, 255, 255})
	// Paint distinct pixel in Right Arm Front (44, 20)
	legacy.SetNRGBA(44, 20, color.NRGBA{0, 255, 0, 255})

	up := Upgrade64x32To64x64(legacy)
	if up.Bounds().Dx() != 64 || up.Bounds().Dy() != 64 {
		t.Fatalf("unexpected dimensions: %v", up.Bounds())
	}
	// Left Leg Front is at (20..23, 52..63), horizontally flipped so x=4 maps to x=23, x=7 maps to x=20.
	if got := up.NRGBAAt(23, 52); got != (color.NRGBA{255, 0, 0, 255}) {
		t.Fatalf("expected red at mirrored (23,52), got %v", got)
	}
	if got := up.NRGBAAt(20, 52); got != (color.NRGBA{0, 0, 255, 255}) {
		t.Fatalf("expected blue at mirrored (20,52), got %v", got)
	}
	// Right Arm Front x=44 -> Left Arm Front x=39.
	if got := up.NRGBAAt(39, 52); got != (color.NRGBA{0, 255, 0, 255}) {
		t.Fatalf("expected green at mirrored (39,52), got %v", got)
	}

	// Verify HD 128x64 -> 128x128 scaling preserves size.
	hd := image.NewNRGBA(image.Rect(0, 0, 128, 64))
	hdUp := Upgrade64x32To64x64(hd)
	if hdUp.Bounds().Dx() != 128 || hdUp.Bounds().Dy() != 128 {
		t.Fatalf("expected 128x128 upgrade, got %v", hdUp.Bounds())
	}
}

func TestConvertArmModelRoundTrip64And128(t *testing.T) {
	classic := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	fillSolidRect(classic, 40, 20, 4, 12, color.NRGBA{10, 20, 30, 255})   // R Arm Outer
	fillSolidRect(classic, 44, 20, 4, 12, color.NRGBA{200, 10, 10, 255})  // R Arm Front
	fillSolidRect(classic, 48, 20, 4, 12, color.NRGBA{10, 200, 10, 255})  // R Arm Inner
	fillSolidRect(classic, 52, 20, 4, 12, color.NRGBA{10, 10, 200, 255})  // R Arm Back

	slim := ConvertArmModel(classic, true)
	// Inner face moves from x=48..51 to x=47..50.
	if got := slim.NRGBAAt(47, 20); got != (color.NRGBA{10, 200, 10, 255}) {
		t.Fatalf("expected inner pixel at (47,20), got %v", got)
	}
	// Back face shrinks to x=51..53; unused x=54..55 must be transparent.
	if got := slim.NRGBAAt(51, 20); got != (color.NRGBA{10, 10, 200, 255}) {
		t.Fatalf("expected back pixel at (51,20), got %v", got)
	}
	if got := slim.NRGBAAt(54, 20); got.A != 0 || slim.NRGBAAt(55, 20).A != 0 {
		t.Fatalf("expected cleared unused columns in slim skin, got (54,20)=%v (55,20)=%v", got, slim.NRGBAAt(55, 20))
	}

	backToClassic := ConvertArmModel(slim, false)
	if got := backToClassic.NRGBAAt(48, 20); got != (color.NRGBA{10, 200, 10, 255}) {
		t.Fatalf("expected restored inner pixel at (48,20), got %v", got)
	}
	if got := backToClassic.NRGBAAt(55, 20); got != (color.NRGBA{10, 10, 200, 255}) {
		t.Fatalf("expected restored back pixel at (55,20), got %v", got)
	}

	hdClassic := ScaleNearest(classic, 128, 128)
	hdSlim := ConvertArmModel(hdClassic, true)
	if got := hdSlim.NRGBAAt(94, 40); got != (color.NRGBA{10, 200, 10, 255}) {
		t.Fatalf("expected HD inner pixel shifted to (94,40), got %v", got)
	}
	if got := hdSlim.NRGBAAt(110, 40); got.A != 0 {
		t.Fatalf("expected cleared unused HD column at (110,40), got %v", got)
	}
}

func TestCompositeStripAdjustCleanAndRender(t *testing.T) {
	base := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	fillSolidRect(base, 8, 8, 8, 8, color.NRGBA{220, 180, 140, 255})   // Head front
	fillSolidRect(base, 40, 8, 8, 8, color.NRGBA{255, 0, 0, 128})      // Hat translucent
	fillSolidRect(base, 20, 20, 8, 12, color.NRGBA{30, 80, 200, 255})  // Torso front
	fillSolidRect(base, 32, 20, 8, 12, color.NRGBA{80, 30, 200, 255})  // Torso back
	base.SetNRGBA(0, 0, color.NRGBA{255, 255, 255, 8})                 // Alpha noise

	ov := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	ov.SetNRGBA(8, 8, color.NRGBA{0, 255, 0, 255})
	comp := CompositeOverlay(base, ov, 0.5)
	if comp.NRGBAAt(8, 8).G <= base.NRGBAAt(8, 8).G {
		t.Fatalf("expected overlay blend to boost green channel, got %v", comp.NRGBAAt(8, 8))
	}

	cleaned := CleanAlphaNoise(base, 16)
	if cleaned.NRGBAAt(0, 0).A != 0 {
		t.Fatalf("expected alpha noise cleared, got %v", cleaned.NRGBAAt(0, 0))
	}

	stripped := StripOuterLayers(base)
	if stripped.NRGBAAt(40, 8).A != 0 {
		t.Fatalf("expected hat outer layer stripped, got %v", stripped.NRGBAAt(40, 8))
	}

	gray := AdjustColors(base, 0, 0, 0, true)
	gc := gray.NRGBAAt(20, 20)
	if gc.R != gc.G || gc.G != gc.B {
		t.Fatalf("expected grayscale equal RGB channels, got %v", gc)
	}

	head := RenderHead(base, 32, true)
	if head.Bounds().Dx() != 32 || head.Bounds().Dy() != 32 {
		t.Fatalf("unexpected head render size: %v", head.Bounds())
	}
	front := RenderBodyFront(base, 4, true, true)
	back := RenderBodyBack(base, 4, true, true)
	if front.Bounds().Dx() != 64 || front.Bounds().Dy() != 128 || back.Bounds().Dx() != 64 || back.Bounds().Dy() != 128 {
		t.Fatalf("unexpected body preview dims: front=%v back=%v", front.Bounds(), back.Bounds())
	}
}
