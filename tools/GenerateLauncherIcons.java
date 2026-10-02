import java.awt.AlphaComposite;
import java.awt.Color;
import java.awt.Graphics2D;
import java.awt.RenderingHints;
import java.awt.image.BufferedImage;
import java.nio.file.Files;
import java.nio.file.Path;
import javax.imageio.ImageIO;

/** Run from the repository root: java tools/GenerateLauncherIcons.java [source.png] */
class GenerateLauncherIcons {
    public static void main(String[] args) throws Exception {
        Path source = Path.of(args.length > 0 ? args[0] : "artwork/bbox.png");
        BufferedImage original = ImageIO.read(source.toFile());
        if (original == null || original.getWidth() != original.getHeight()) {
            throw new IllegalArgumentException("Expected a square PNG: " + source);
        }
        Path res = Path.of("app/src/main/res");
        String[] densities = {"mdpi", "hdpi", "xhdpi", "xxhdpi", "xxxhdpi"};
        int[] sizes = {48, 72, 96, 144, 192};
        for (int i = 0; i < sizes.length; i++) {
            BufferedImage icon = fit(original, sizes[i], sizes[i], true);
            save(icon, res.resolve("mipmap-" + densities[i] + "/ic_launcher.png"));
            BufferedImage mask = new BufferedImage(sizes[i], sizes[i], BufferedImage.TYPE_INT_ARGB);
            Graphics2D g = graphics(mask);
            g.setColor(Color.WHITE);
            g.fillOval(0, 0, sizes[i], sizes[i]);
            g.dispose();
            g = graphics(icon);
            g.setComposite(AlphaComposite.DstIn);
            g.drawImage(mask, 0, 0, null);
            g.dispose();
            save(icon, res.resolve("mipmap-" + densities[i] + "/ic_launcher_round.png"));
        }
        // 108dp adaptive layer at 4x, with artwork inside the central 72dp.
        save(fit(original, 432, 288, true), res.resolve("drawable-nodpi/ic_launcher_foreground.png"));
        BufferedImage monochrome = new BufferedImage(original.getWidth(), original.getHeight(), BufferedImage.TYPE_INT_ARGB);
        for (int y = 0; y < original.getHeight(); y++) {
            for (int x = 0; x < original.getWidth(); x++) {
                int pixel = original.getRGB(x, y);
                int red = (pixel >> 16) & 255;
                // Keep the blue artwork; white and pale globe lines become cutouts.
                int alpha = Math.min(255, Math.max(0, (200 - red) * 255 / 48));
                alpha = alpha * ((pixel >>> 24) & 255) / 255;
                monochrome.setRGB(x, y, (alpha << 24) | 0xffffff);
            }
        }
        save(fit(monochrome, 432, 288, false), res.resolve("drawable-nodpi/ic_launcher_monochrome.png"));
        save(fit(monochrome, 96, 96, false), res.resolve("drawable/ic_menu.png"));
        System.out.println("Generated launcher, round, adaptive, themed and notification icons from " + source);
    }

    private static BufferedImage fit(BufferedImage source, int size, int contentSize, boolean white) {
        BufferedImage result = new BufferedImage(size, size, BufferedImage.TYPE_INT_ARGB);
        Graphics2D g = graphics(result);
        if (white) {
            g.setColor(Color.WHITE);
            g.fillRect(0, 0, size, size);
        }
        int inset = (size - contentSize) / 2;
        g.drawImage(source, inset, inset, contentSize, contentSize, null);
        g.dispose();
        return result;
    }

    private static Graphics2D graphics(BufferedImage image) {
        Graphics2D g = image.createGraphics();
        g.setRenderingHint(RenderingHints.KEY_INTERPOLATION, RenderingHints.VALUE_INTERPOLATION_BICUBIC);
        g.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON);
        return g;
    }

    private static void save(BufferedImage image, Path path) throws Exception {
        Files.createDirectories(path.getParent());
        ImageIO.write(image, "png", path.toFile());
    }
}
