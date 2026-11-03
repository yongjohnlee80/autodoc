// Draws AutoDoc's 1024x1024 app icon: a document with a magnifying glass
// over a highlighted line, on a macOS-style rounded-square tile.
import AppKit

let size: CGFloat = 1024
let out = CommandLine.arguments[1]

func rgb(_ hex: UInt32, _ a: CGFloat = 1) -> NSColor {
    NSColor(srgbRed: CGFloat((hex >> 16) & 0xff) / 255,
            green: CGFloat((hex >> 8) & 0xff) / 255,
            blue: CGFloat(hex & 0xff) / 255, alpha: a)
}

let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(size), pixelsHigh: Int(size),
                           bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                           colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
let ctx = NSGraphicsContext.current!.cgContext

// Tile: Apple's grid puts an 824pt body in the 1024 canvas.
let tile = NSRect(x: 100, y: 100, width: 824, height: 824)
let tilePath = NSBezierPath(roundedRect: tile, xRadius: 185, yRadius: 185)
ctx.saveGState()
let shadow = NSShadow()
shadow.shadowColor = rgb(0x000000, 0.35)
shadow.shadowBlurRadius = 28
shadow.shadowOffset = NSSize(width: 0, height: -12)
shadow.set()
rgb(0x24308f).setFill()
tilePath.fill()
ctx.restoreGState()
NSGradient(colors: [rgb(0x5b6cf2), rgb(0x3341c4), rgb(0x1d2580)],
           atLocations: [0, 0.55, 1], colorSpace: .sRGB)!.draw(in: tilePath, angle: -90)
// A soft sheen on the upper half.
ctx.saveGState()
tilePath.addClip()
NSGradient(starting: rgb(0xffffff, 0.16), ending: rgb(0xffffff, 0))!
    .draw(in: NSRect(x: 100, y: 512, width: 824, height: 412), angle: -90)
ctx.restoreGState()

// Page with a folded top-right corner.
let page = NSRect(x: 262, y: 236, width: 400, height: 540)
let fold: CGFloat = 104
let pagePath = NSBezierPath()
let r: CGFloat = 34
pagePath.move(to: NSPoint(x: page.minX + r, y: page.minY))
pagePath.line(to: NSPoint(x: page.maxX - r, y: page.minY))
pagePath.curve(to: NSPoint(x: page.maxX, y: page.minY + r),
               controlPoint1: NSPoint(x: page.maxX, y: page.minY), controlPoint2: NSPoint(x: page.maxX, y: page.minY))
pagePath.line(to: NSPoint(x: page.maxX, y: page.maxY - fold))
pagePath.line(to: NSPoint(x: page.maxX - fold, y: page.maxY))
pagePath.line(to: NSPoint(x: page.minX + r, y: page.maxY))
pagePath.curve(to: NSPoint(x: page.minX, y: page.maxY - r),
               controlPoint1: NSPoint(x: page.minX, y: page.maxY), controlPoint2: NSPoint(x: page.minX, y: page.maxY))
pagePath.line(to: NSPoint(x: page.minX, y: page.minY + r))
pagePath.curve(to: NSPoint(x: page.minX + r, y: page.minY),
               controlPoint1: NSPoint(x: page.minX, y: page.minY), controlPoint2: NSPoint(x: page.minX, y: page.minY))
pagePath.close()
ctx.saveGState()
let pageShadow = NSShadow()
pageShadow.shadowColor = rgb(0x0a0f40, 0.45)
pageShadow.shadowBlurRadius = 30
pageShadow.shadowOffset = NSSize(width: 0, height: -14)
pageShadow.set()
rgb(0xfbfbff).setFill()
pagePath.fill()
ctx.restoreGState()

let foldPath = NSBezierPath()
foldPath.move(to: NSPoint(x: page.maxX - fold, y: page.maxY))
foldPath.line(to: NSPoint(x: page.maxX - fold, y: page.maxY - fold + 18))
foldPath.curve(to: NSPoint(x: page.maxX - fold + 18, y: page.maxY - fold),
               controlPoint1: NSPoint(x: page.maxX - fold, y: page.maxY - fold),
               controlPoint2: NSPoint(x: page.maxX - fold, y: page.maxY - fold))
foldPath.line(to: NSPoint(x: page.maxX, y: page.maxY - fold))
foldPath.close()
rgb(0xd5d9f5).setFill()
foldPath.fill()

// Text: a heading with a Markdown '#' mark, then body lines.
func bar(_ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat, _ c: NSColor) {
    c.setFill()
    NSBezierPath(roundedRect: NSRect(x: x, y: y, width: w, height: h), xRadius: h / 2, yRadius: h / 2).fill()
}
let left = page.minX + 52
let hash = NSAttributedString(string: "#", attributes: [
    .font: NSFont.systemFont(ofSize: 92, weight: .heavy),
    .foregroundColor: rgb(0x3d4ee0),
])
hash.draw(at: NSPoint(x: left - 4, y: 632))
bar(left + 66, 664, 170, 30, rgb(0x2a3399))
let body: [(CGFloat, CGFloat)] = [(586, 296), (536, 262), (486, 296), (436, 214), (386, 280), (336, 180)]
for (y, w) in body { bar(left, y, w, 22, rgb(0xc3c8e6)) }

// Magnifying glass over the lower-right of the page.
let c = NSPoint(x: 640, y: 392)
let lensR: CGFloat = 138
let ring: CGFloat = 36

// Handle first, so the ring covers its end.
ctx.saveGState()
let handleShadow = NSShadow()
handleShadow.shadowColor = rgb(0x0a0f40, 0.5)
handleShadow.shadowBlurRadius = 24
handleShadow.shadowOffset = NSSize(width: 0, height: -10)
handleShadow.set()
let handle = NSBezierPath()
handle.move(to: NSPoint(x: c.x + 0.70 * (lensR + 6), y: c.y - 0.70 * (lensR + 6)))
handle.line(to: NSPoint(x: 838, y: 194))
handle.lineWidth = 66
handle.lineCapStyle = .round
rgb(0xf0a531).setStroke()
handle.stroke()
ctx.restoreGState()
let handleHi = NSBezierPath()
handleHi.move(to: NSPoint(x: c.x + 0.70 * (lensR + 30), y: c.y - 0.70 * (lensR + 30) + 14))
handleHi.line(to: NSPoint(x: 826, y: 222))
handleHi.lineWidth = 14
handleHi.lineCapStyle = .round
rgb(0xffd27a, 0.9).setStroke()
handleHi.stroke()

// Lens: the page seen through it, magnified, with the matched line lit.
let lens = NSBezierPath(ovalIn: NSRect(x: c.x - lensR, y: c.y - lensR, width: lensR * 2, height: lensR * 2))
ctx.saveGState()
lens.addClip()
rgb(0xeef1ff).setFill()
lens.fill()
bar(c.x - 150, c.y + 58, 230, 34, rgb(0xc3c8e6))
bar(c.x - 150, c.y - 17, 260, 34, rgb(0xffd166))
bar(c.x - 150, c.y - 17, 180, 34, rgb(0xf0a531))
bar(c.x - 150, c.y - 92, 200, 34, rgb(0xc3c8e6))
NSGradient(starting: rgb(0xffffff, 0.55), ending: rgb(0xffffff, 0))!
    .draw(in: NSRect(x: c.x - lensR, y: c.y, width: lensR * 2, height: lensR), angle: -90)
ctx.restoreGState()

let ringPath = NSBezierPath(ovalIn: NSRect(x: c.x - lensR, y: c.y - lensR, width: lensR * 2, height: lensR * 2))
ringPath.lineWidth = ring
ctx.saveGState()
let ringShadow = NSShadow()
ringShadow.shadowColor = rgb(0x0a0f40, 0.35)
ringShadow.shadowBlurRadius = 16
ringShadow.shadowOffset = NSSize(width: 0, height: -6)
ringShadow.set()
rgb(0x1b2270).setStroke()
ringPath.stroke()
ctx.restoreGState()
let ringHi = NSBezierPath(ovalIn: NSRect(x: c.x - lensR + 10, y: c.y - lensR + 10,
                                         width: (lensR - 10) * 2, height: (lensR - 10) * 2))
ringHi.lineWidth = 6
rgb(0x7f8cff, 0.8).setStroke()
ringHi.stroke()

NSGraphicsContext.restoreGraphicsState()
try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: out))
