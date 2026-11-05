// makeicon.swift —— 生成雨课助手应用图标（1024×1024 PNG）
//
// 用法：swiftc -O -o tools/bin/makeicon tools/makeicon.swift -framework AppKit
//       tools/bin/makeicon build/appicon.png [paper|ink] [SemiBold|Bold|Medium]
//
// 视觉方向：「文隐」——纯白（或纯黑）圆角底 + 思源宋体单字「雨」。
// 不画边界线、圆点、渐变或任何装饰；字形按「墨迹包围盒」做光学居中，
// 让字到四边的留白尽量等距（中文单字的字面重心与字体度量框不一致）。
//
// 字体优先级：思源宋体 SC → Noto Serif SC → 宋体-简；都没有时退回系统字体。
// 仓库只需要提交 PNG，不提交字体文件。

import AppKit
import Foundation

let args = CommandLine.arguments
let outPath = args.count > 1 ? args[1] : "appicon.png"
let variant = args.count > 2 ? args[2] : "paper"   // paper = 白底黑字，ink = 黑底白字
let weight = args.count > 3 ? args[3] : "SemiBold"
let size: CGFloat = 1024
let contentInset: CGFloat = 100
let contentSide: CGFloat = 824
let radius: CGFloat = 186
let targetInkSide: CGFloat = 520   // 字面墨迹最长边，四边留白约 152–158px

func color(_ r: CGFloat, _ g: CGFloat, _ b: CGFloat, _ a: CGFloat = 1) -> NSColor {
    NSColor(srgbRed: r / 255, green: g / 255, blue: b / 255, alpha: a)
}

// 解析字体名（只返回实际存在的 PostScript 名）。
func resolveFontName(_ weight: String) -> String {
    let candidates = [
        "SourceHanSerifSC-" + weight,
        "SourceHanSerifSC-Regular",
        "NotoSerifSC-" + weight,
        "NotoSerifSC-Regular",
        "STSongti-SC-Bold",
    ]
    for name in candidates where NSFont(name: name, size: 20) != nil {
        return name
    }
    return NSFont.systemFont(ofSize: 20, weight: .semibold).fontName
}

// 测量「超」的墨迹包围盒：返回宽高与相对绘制原点的偏移（左下原点坐标系）。
func inkMetrics(fontName: String, fontSize: CGFloat) -> (w: CGFloat, h: CGFloat, offX: CGFloat, offY: CGFloat) {
    let canvas: CGFloat = 1400
    guard let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil, pixelsWide: Int(canvas), pixelsHigh: Int(canvas),
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
    ) else { return (0, 0, 0, 0) }
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    NSColor.clear.setFill()
    NSRect(x: 0, y: 0, width: canvas, height: canvas).fill()
    let font = NSFont(name: fontName, size: fontSize) ?? NSFont.systemFont(ofSize: fontSize)
    let origin = NSPoint(x: 300, y: 300)
    ("雨" as NSString).draw(at: origin, withAttributes: [.font: font, .foregroundColor: NSColor.black])
    NSGraphicsContext.restoreGraphicsState()
    guard let data = rep.bitmapData else { return (0, 0, 0, 0) }
    let bpr = rep.bytesPerRow, bpp = rep.bitsPerPixel / 8, imgH = rep.pixelsHigh
    var minX = rep.pixelsWide, maxX = -1, minYTop = imgH, maxYTop = -1
    for y in 0..<imgH {
        for x in 0..<rep.pixelsWide {
            if data[y * bpr + x * bpp + 3] > 12 {
                if x < minX { minX = x }
                if x > maxX { maxX = x }
                if y < minYTop { minYTop = y }
                if y > maxYTop { maxYTop = y }
            }
        }
    }
    if maxX < 0 { return (0, 0, 0, 0) }
    let w = CGFloat(maxX - minX + 1)
    let h = CGFloat(maxYTop - minYTop + 1)
    let bottom = imgH - 1 - maxYTop
    return (w, h, CGFloat(minX) - origin.x, CGFloat(bottom) - origin.y)
}

let white = color(255, 255, 255)
let black = color(0, 0, 0)
let bg = variant == "ink" ? black : white
let glyph = variant == "ink" ? white : black
let fontName = resolveFontName(weight)

// 先用大字号测量墨迹，再等比缩放到目标最长边，并让墨迹包围盒中心落在画布中心。
let probeSize: CGFloat = 600
let metrics = inkMetrics(fontName: fontName, fontSize: probeSize)
let maxSide = max(metrics.w, metrics.h)
let scale = maxSide > 0 ? targetInkSide / maxSide : 1
let finalFontSize = probeSize * scale
let finalFont = NSFont(name: fontName, size: finalFontSize) ?? NSFont.systemFont(ofSize: finalFontSize)
let drawX = (size - metrics.w * scale) / 2 - metrics.offX * scale
let drawY = (size - metrics.h * scale) / 2 - metrics.offY * scale

guard let rep = NSBitmapImageRep(
    bitmapDataPlanes: nil, pixelsWide: 1024, pixelsHigh: 1024,
    bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
    colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
) else { exit(1) }
rep.size = NSSize(width: size, height: size)
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
NSColor.clear.setFill()
NSRect(x: 0, y: 0, width: size, height: size).fill()

// 纯白/纯黑圆角底
let content = NSRect(x: contentInset, y: contentInset, width: contentSide, height: contentSide)
let tile = NSBezierPath(roundedRect: content, xRadius: radius, yRadius: radius)
bg.setFill()
tile.fill()

// 单字「雨」：黑色思源宋体（暗色版反白）
let attrs: [NSAttributedString.Key: Any] = [.font: finalFont, .foregroundColor: glyph]
("雨" as NSString).draw(at: NSPoint(x: drawX, y: drawY), withAttributes: attrs)
NSGraphicsContext.restoreGraphicsState()

// 自检：按「与底色不同」找字形墨迹，打印到内容区四边的实际留白。
func finalMargins() -> String {
    guard let data = rep.bitmapData else { return "?" }
    let bpr = rep.bytesPerRow, bpp = rep.bitsPerPixel / 8, imgH = rep.pixelsHigh
    let bgR = bg.redComponent * 255, bgG = bg.greenComponent * 255, bgB = bg.blueComponent * 255
    var minX = rep.pixelsWide, maxX = -1, minYTop = imgH, maxYTop = -1
    let contentMinX = Int(content.minX), contentMaxX = Int(content.maxX)
    let contentMinY = Int(content.minY), contentMaxY = Int(content.maxY)
    let scanX0 = Int(content.minX), scanX1 = Int(content.maxX)
    let scanY0 = imgH - Int(content.maxY), scanY1 = imgH - Int(content.minY)
    for y in scanY0..<scanY1 {
        for x in scanX0..<scanX1 {
            let i = y * bpr + x * bpp
            let a = data[i + 3]
            if a < 250 { continue }   // 跳过圆角/字形抗锯齿的预乘像素
            let r = Float(data[i]), g = Float(data[i + 1]), b = Float(data[i + 2])
            let diff = abs(r - Float(bgR)) + abs(g - Float(bgG)) + abs(b - Float(bgB))
            if diff > 90 {
                if x < minX { minX = x }
                if x > maxX { maxX = x }
                if y < minYTop { minYTop = y }
                if y > maxYTop { maxYTop = y }
            }
        }
    }
    let left = CGFloat(minX) - content.minX
    let right = content.maxX - CGFloat(maxX)
    let top = content.maxY - CGFloat(imgH - 1 - minYTop)
    let bottom = CGFloat(imgH - 1 - maxYTop) - content.minY
    _ = (contentMinX, contentMaxX, contentMinY, contentMaxY)
    return String(format: "margins L/R/T/B = %.1f/%.1f/%.1f/%.1f", left, right, top, bottom)
}

guard let png = rep.representation(using: .png, properties: [:]) else { exit(1) }
do {
    try png.write(to: URL(fileURLWithPath: outPath))
} catch {
    FileHandle.standardError.write("makeicon: 写入失败 \(error)\n".data(using: .utf8)!)
    exit(1)
}
print("\(outPath) \(rep.pixelsWide)x\(rep.pixelsHigh) font=\(finalFont.fontName) size=\(String(format: "%.1f", finalFontSize)) \(finalMargins())")
