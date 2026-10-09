//! Playback of animated previews: video stickers, GIFs and videos.
//!
//! The server renders every moving media into a looping animated WebP. Here it
//! is decoded into frames, uploaded to the GPU once, and played back by picking
//! the frame that matches the current time - nothing is re-uploaded per frame.

use std::collections::{HashMap, HashSet};
use std::io::Cursor;
use std::time::{Duration, Instant};

use anyhow::{bail, Context, Result};
use eframe::egui::{self, ColorImage, TextureHandle};
use image::codecs::webp::WebPDecoder;
use image::imageops::{self, FilterType};
use image::{AnimationDecoder, RgbaImage};

/// The selected-sticker panel shows previews up to 220pt, so the full preview
/// resolution the server renders is enough there.
pub const SELECTED_SIDE: u32 = 256;
/// Frames are never decoded smaller than this, whatever the cell size.
const MIN_GRID_SIDE: u32 = 64;
/// Delays below this are rounded up, as browsers do, so a broken file cannot
/// make playback spin.
const MIN_FRAME_DELAY_MS: u32 = 20;
const DEFAULT_FRAME_DELAY_MS: u32 = 100;
/// Decoded frames live in GPU memory. A screen full of video stickers easily
/// reaches this, at which point the rest simply stay static.
const GRID_BUDGET_BYTES: usize = 192 * 1024 * 1024;
/// A preview the server has not rendered yet is asked for again after this.
const MISSING_RETRY: Duration = Duration::from_secs(120);
/// Frames per loop assumed when estimating the cost of a not-yet-loaded preview.
const ESTIMATED_FRAMES: usize = 45;
/// How often to repaint while anything animates: a bit above the 15fps the
/// previews are rendered at, so frame changes are not visibly late.
pub const REPAINT_INTERVAL: Duration = Duration::from_millis(40);

/// Why an animation is being loaded: grid cells are decoded at cell size,
/// the selected panel at full preview size.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Purpose {
    Grid,
    Selected,
}

pub struct DecodedFrames {
    pub frames: Vec<ColorImage>,
    pub delays_ms: Vec<u32>,
    pub side: u32,
}

/// Decodes an animated WebP, shrinking frames so their longer side is at most
/// `max_side`. Static WebPs come back as a single frame.
pub fn decode(bytes: &[u8], max_side: u32) -> Result<DecodedFrames> {
    let decoder = WebPDecoder::new(Cursor::new(bytes)).context("not a WebP")?;
    let mut frames = Vec::new();
    let mut delays_ms = Vec::new();
    let mut side = 0;
    let mut push = |buffer: RgbaImage, delay_ms: u32| {
        let buffer = shrink(buffer, max_side);
        side = side.max(buffer.width().max(buffer.height()));
        let size = [buffer.width() as usize, buffer.height() as usize];
        frames.push(ColorImage::from_rgba_unmultiplied(size, buffer.as_raw()));
        delays_ms.push(delay_ms);
    };
    if decoder.has_animation() {
        for frame in decoder.into_frames() {
            let frame = frame.context("corrupt animation frame")?;
            let delay = frame_delay_ms(frame.delay().numer_denom_ms());
            push(frame.into_buffer(), delay);
        }
    } else {
        // A still WebP yields no frames at all through `into_frames`, so it
        // has to be read as a plain image.
        let still = image::DynamicImage::from_decoder(decoder).context("corrupt still WebP")?;
        push(still.into_rgba8(), DEFAULT_FRAME_DELAY_MS);
    }
    if frames.is_empty() {
        bail!("animation has no frames");
    }
    Ok(DecodedFrames {
        frames,
        delays_ms,
        side,
    })
}

fn frame_delay_ms((numer, denom): (u32, u32)) -> u32 {
    if denom == 0 || numer == 0 {
        return DEFAULT_FRAME_DELAY_MS;
    }
    (numer / denom).max(MIN_FRAME_DELAY_MS)
}

fn shrink(buffer: RgbaImage, max_side: u32) -> RgbaImage {
    let longest = buffer.width().max(buffer.height());
    if longest <= max_side || max_side == 0 {
        return buffer;
    }
    let scale = max_side as f32 / longest as f32;
    let width = ((buffer.width() as f32 * scale).round() as u32).max(1);
    let height = ((buffer.height() as f32 * scale).round() as u32).max(1);
    imageops::resize(&buffer, width, height, FilterType::Triangle)
}

/// Which frame is on screen `elapsed_ms` into the loop. `ends_ms` holds the
/// cumulative end time of each frame.
fn frame_index(ends_ms: &[u64], elapsed_ms: u64) -> usize {
    let Some(&total) = ends_ms.last() else {
        return 0;
    };
    if total == 0 {
        return 0;
    }
    let t = elapsed_ms % total;
    ends_ms
        .partition_point(|&end| end <= t)
        .min(ends_ms.len() - 1)
}

/// Frame size a grid cell of `thumb_points` needs on a display with
/// `pixels_per_point`, rounded up to a 32px step so small slider moves do not
/// trigger a re-decode. Previews are 256px, so asking for more is pointless.
pub fn grid_side(thumb_points: f32, pixels_per_point: f32) -> u32 {
    let pixels = (thumb_points * pixels_per_point).ceil().max(1.0) as u32;
    pixels
        .div_ceil(32)
        .saturating_mul(32)
        .clamp(MIN_GRID_SIDE, SELECTED_SIDE)
}

/// An animation uploaded to the GPU.
pub struct Animation {
    frames: Vec<TextureHandle>,
    ends_ms: Vec<u64>,
    side: u32,
    bytes: usize,
}

impl Animation {
    pub fn upload(ctx: &egui::Context, name: &str, decoded: DecodedFrames) -> Self {
        let mut ends_ms = Vec::with_capacity(decoded.delays_ms.len());
        let mut elapsed = 0u64;
        for delay in &decoded.delays_ms {
            elapsed += u64::from(*delay);
            ends_ms.push(elapsed);
        }
        let bytes = decoded
            .frames
            .iter()
            .map(|frame| frame.pixels.len() * 4)
            .sum();
        let frames = decoded
            .frames
            .into_iter()
            .enumerate()
            .map(|(index, frame)| {
                ctx.load_texture(
                    format!("{name}#{index}"),
                    frame,
                    egui::TextureOptions::LINEAR,
                )
            })
            .collect();
        Self {
            frames,
            ends_ms,
            side: decoded.side,
            bytes,
        }
    }

    pub fn frame_at(&self, elapsed: Duration) -> &TextureHandle {
        &self.frames[frame_index(&self.ends_ms, elapsed.as_millis() as u64)]
    }
}

/// Keeps grid animations within a GPU budget and decides what to fetch next.
///
/// An animation that is on screen is never evicted: with more visible video
/// stickers than the budget allows, the remainder stays static rather than
/// endlessly swapping in and out, which would burn CPU on re-decoding.
pub struct Animations {
    clock: Instant,
    grid: HashMap<String, Animation>,
    grid_bytes: usize,
    budget_bytes: usize,
    last_seen: HashMap<String, u64>,
    frame: u64,
    selected: Option<(String, Animation)>,
    pending: HashSet<(String, Purpose)>,
    missing: HashMap<String, Instant>,
    // Previews with a single frame: there is nothing to play, and the static
    // thumbnail already shows that frame (with transparency, which a
    // single-frame WebP from ffmpeg loses), so they are never fetched again.
    still: HashSet<String>,
    drew_animation: bool,
}

impl Default for Animations {
    fn default() -> Self {
        Self::with_budget(GRID_BUDGET_BYTES)
    }
}

impl Animations {
    fn with_budget(budget_bytes: usize) -> Self {
        Self {
            clock: Instant::now(),
            grid: HashMap::new(),
            grid_bytes: 0,
            budget_bytes,
            last_seen: HashMap::new(),
            frame: 0,
            selected: None,
            pending: HashSet::new(),
            missing: HashMap::new(),
            still: HashSet::new(),
            drew_animation: false,
        }
    }

    /// Starts a UI frame.
    pub fn begin_frame(&mut self) {
        self.frame += 1;
        self.drew_animation = false;
    }

    /// Whether this frame showed anything moving. Checked after drawing, so
    /// the very first frame an animation appears in already schedules the
    /// next one - checking the previous frame instead would leave a freshly
    /// loaded animation frozen until something else woke the UI up.
    pub fn is_moving(&self) -> bool {
        self.drew_animation
    }

    /// Current frame of every loaded grid animation, keyed by file id.
    pub fn grid_frames(&self) -> HashMap<String, TextureHandle> {
        let elapsed = self.clock.elapsed();
        self.grid
            .iter()
            .map(|(id, animation)| (id.clone(), animation.frame_at(elapsed).clone()))
            .collect()
    }

    /// Records that these animated cells are on screen and returns those that
    /// should be fetched now at `side`.
    pub fn visible(&mut self, file_ids: &[String], side: u32) -> Vec<String> {
        let mut wanted = Vec::new();
        for id in file_ids {
            self.last_seen.insert(id.clone(), self.frame);
            if let Some(animation) = self.grid.get(id) {
                self.drew_animation = true;
                // A decode made for a smaller cell would be upscaled and blurry.
                if animation.side >= side {
                    continue;
                }
            }
            if self.should_fetch(id, Purpose::Grid) {
                wanted.push(id.clone());
            }
        }
        // Only fetch what the budget can hold once off-screen previews are freed.
        let estimate = estimated_bytes(side);
        let mut room = self.budget_bytes.saturating_sub(self.grid_bytes) + self.evictable_bytes();
        wanted.retain(|_| {
            if room >= estimate {
                room -= estimate;
                true
            } else {
                false
            }
        });
        for id in &wanted {
            self.pending.insert((id.clone(), Purpose::Grid));
        }
        wanted
    }

    /// Whether the selected panel needs `file_id` fetched now.
    pub fn want_selected(&mut self, file_id: &str) -> bool {
        if matches!(&self.selected, Some((id, _)) if id == file_id) {
            return false;
        }
        if !self.should_fetch(file_id, Purpose::Selected) {
            return false;
        }
        self.pending
            .insert((file_id.to_string(), Purpose::Selected));
        true
    }

    pub fn selected_frame(&mut self, file_id: &str) -> Option<TextureHandle> {
        let (id, animation) = self.selected.as_ref()?;
        if id != file_id {
            return None;
        }
        self.drew_animation = true;
        Some(animation.frame_at(self.clock.elapsed()).clone())
    }

    fn should_fetch(&self, file_id: &str, purpose: Purpose) -> bool {
        if self.still.contains(file_id) || self.pending.contains(&(file_id.to_string(), purpose)) {
            return false;
        }
        match self.missing.get(file_id) {
            Some(since) => since.elapsed() >= MISSING_RETRY,
            None => true,
        }
    }

    pub fn loaded(&mut self, file_id: String, purpose: Purpose, animation: Animation) {
        self.pending.remove(&(file_id.clone(), purpose));
        self.missing.remove(&file_id);
        match purpose {
            Purpose::Selected => self.selected = Some((file_id, animation)),
            Purpose::Grid => {
                if let Some(old) = self.grid.remove(&file_id) {
                    self.grid_bytes -= old.bytes;
                }
                self.make_room(animation.bytes);
                self.grid_bytes += animation.bytes;
                self.grid.insert(file_id, animation);
            }
        }
    }

    /// The server has no preview (yet) or it could not be decoded: keep the
    /// static thumbnail and do not ask again for a while.
    pub fn missing(&mut self, file_id: String, purpose: Purpose) {
        self.pending.remove(&(file_id.clone(), purpose));
        self.missing.insert(file_id, Instant::now());
    }

    /// The preview turned out to be a single frame: keep the thumbnail.
    pub fn still(&mut self, file_id: String, purpose: Purpose) {
        self.pending.remove(&(file_id.clone(), purpose));
        self.still.insert(file_id);
    }

    fn is_on_screen(&self, file_id: &str) -> bool {
        // Seen this frame or the previous one: the grid reports visibility
        // after drawing, so the current frame may not be recorded yet.
        self.last_seen
            .get(file_id)
            .is_some_and(|&seen| seen + 1 >= self.frame)
    }

    fn evictable_bytes(&self) -> usize {
        self.grid
            .iter()
            .filter(|(id, _)| !self.is_on_screen(id))
            .map(|(_, animation)| animation.bytes)
            .sum()
    }

    fn make_room(&mut self, incoming: usize) {
        let mut victims: Vec<(u64, String)> = self
            .grid
            .keys()
            .filter(|id| !self.is_on_screen(id))
            .map(|id| (self.last_seen.get(id).copied().unwrap_or(0), id.clone()))
            .collect();
        victims.sort();
        for (_, id) in victims {
            if self.grid_bytes + incoming <= self.budget_bytes {
                break;
            }
            if let Some(old) = self.grid.remove(&id) {
                self.grid_bytes -= old.bytes;
            }
        }
    }

    #[cfg(test)]
    fn grid_ids(&self) -> HashSet<String> {
        self.grid.keys().cloned().collect()
    }
}

fn estimated_bytes(side: u32) -> usize {
    (side as usize) * (side as usize) * 4 * ESTIMATED_FRAMES
}

#[cfg(test)]
mod tests {
    use super::*;

    const FIXTURE: &[u8] = include_bytes!("testdata/animated.webp");

    #[test]
    fn decodes_every_frame_of_a_real_preview() {
        // The fixture is what the server's ffmpeg produces: 3s at 15fps.
        let decoded = decode(FIXTURE, SELECTED_SIDE).unwrap();
        assert_eq!(decoded.frames.len(), 45);
        assert_eq!(decoded.side, 256);
        assert!(decoded.delays_ms.iter().all(|&d| (60..=70).contains(&d)));
    }

    #[test]
    fn keeps_transparency() {
        let decoded = decode(FIXTURE, SELECTED_SIDE).unwrap();
        let first = &decoded.frames[0];
        assert!(
            first.pixels.iter().any(|p| p.a() == 0),
            "sticker background must stay transparent"
        );
        assert!(
            first.pixels.iter().any(|p| p.a() == 255),
            "the moving shape must stay opaque"
        );
    }

    #[test]
    fn frames_actually_change_over_time() {
        let decoded = decode(FIXTURE, 64).unwrap();
        assert_ne!(
            decoded.frames[0].pixels, decoded.frames[20].pixels,
            "an animation whose frames are identical would look frozen"
        );
    }

    #[test]
    fn shrinks_frames_to_the_requested_side() {
        let decoded = decode(FIXTURE, 96).unwrap();
        assert_eq!(decoded.side, 96);
        assert!(decoded
            .frames
            .iter()
            .all(|f| f.size[0] <= 96 && f.size[1] <= 96));
    }

    #[test]
    fn decodes_a_static_webp_as_one_frame() {
        // Some video stickers are a single frame; ffmpeg then writes a plain
        // still WebP rather than an animation. It must decode, not crash.
        let image = RgbaImage::from_pixel(8, 8, image::Rgba([1, 2, 3, 255]));
        let mut bytes = Vec::new();
        image::codecs::webp::WebPEncoder::new_lossless(&mut bytes)
            .encode(image.as_raw(), 8, 8, image::ExtendedColorType::Rgba8)
            .unwrap();
        let decoded = decode(&bytes, 64).unwrap();
        assert_eq!(decoded.frames.len(), 1);
    }

    #[test]
    fn single_frame_previews_are_never_fetched_again() {
        let mut anims = Animations::default();
        anims.begin_frame();
        anims.visible(&ids(&["s"]), 64);
        anims.still("s".into(), Purpose::Grid);
        anims.begin_frame();
        assert!(anims.visible(&ids(&["s"]), 64).is_empty());
        assert!(!anims.want_selected("s"), "not for the panel either");
    }

    #[test]
    fn rejects_garbage() {
        assert!(decode(b"not an image", 128).is_err());
    }

    #[test]
    fn broken_delays_cannot_make_playback_spin() {
        assert_eq!(frame_delay_ms((0, 1)), DEFAULT_FRAME_DELAY_MS);
        assert_eq!(frame_delay_ms((5, 0)), DEFAULT_FRAME_DELAY_MS);
        assert_eq!(frame_delay_ms((1, 1)), MIN_FRAME_DELAY_MS);
        assert_eq!(frame_delay_ms((67, 1)), 67);
    }

    #[test]
    fn picks_the_frame_for_the_current_time_and_loops() {
        let ends = [100, 200, 300];
        assert_eq!(frame_index(&ends, 0), 0);
        assert_eq!(frame_index(&ends, 99), 0);
        assert_eq!(frame_index(&ends, 100), 1);
        assert_eq!(frame_index(&ends, 299), 2);
        assert_eq!(frame_index(&ends, 300), 0, "must wrap around");
        assert_eq!(frame_index(&ends, 1150), 2, "several loops in");
        assert_eq!(frame_index(&ends, 1250), 0);
        assert_eq!(frame_index(&[], 50), 0);
    }

    #[test]
    fn grid_side_follows_cell_size_and_display_density() {
        assert_eq!(grid_side(100.0, 1.0), 128);
        assert_eq!(grid_side(100.0, 2.0), 224);
        assert_eq!(grid_side(10.0, 1.0), MIN_GRID_SIDE);
        assert_eq!(
            grid_side(200.0, 2.0),
            SELECTED_SIDE,
            "previews are only 256px"
        );
    }

    fn fake_animation(ctx: &egui::Context, side: u32) -> Animation {
        let frame = ColorImage::filled([side as usize, side as usize], egui::Color32::RED);
        Animation::upload(
            ctx,
            "test",
            DecodedFrames {
                frames: vec![frame],
                delays_ms: vec![100],
                side,
            },
        )
    }

    fn ids(names: &[&str]) -> Vec<String> {
        names.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn does_not_ask_twice_while_a_fetch_is_in_flight() {
        let mut anims = Animations::default();
        anims.begin_frame();
        assert_eq!(anims.visible(&ids(&["a"]), 128), ids(&["a"]));
        anims.begin_frame();
        assert!(anims.visible(&ids(&["a"]), 128).is_empty());
    }

    #[test]
    fn missing_preview_is_not_hammered() {
        let mut anims = Animations::default();
        anims.begin_frame();
        anims.visible(&ids(&["a"]), 128);
        anims.missing("a".into(), Purpose::Grid);
        anims.begin_frame();
        assert!(
            anims.visible(&ids(&["a"]), 128).is_empty(),
            "a 404 must not be re-requested every frame"
        );
    }

    #[test]
    fn refetches_when_the_cell_outgrows_the_decode() {
        let ctx = egui::Context::default();
        let mut anims = Animations::default();
        anims.begin_frame();
        anims.visible(&ids(&["a"]), 64);
        anims.loaded("a".into(), Purpose::Grid, fake_animation(&ctx, 64));
        anims.begin_frame();
        assert!(anims.visible(&ids(&["a"]), 64).is_empty());
        anims.begin_frame();
        assert_eq!(
            anims.visible(&ids(&["a"]), 192),
            ids(&["a"]),
            "a 64px decode would look blurry in a 192px cell"
        );
    }

    #[test]
    fn evicts_off_screen_previews_but_never_visible_ones() {
        let ctx = egui::Context::default();
        let one = estimated_bytes(64); // fake frames are far smaller than this
        let mut anims = Animations::with_budget(one * 2);

        anims.begin_frame();
        anims.visible(&ids(&["a", "b"]), 64);
        anims.loaded("a".into(), Purpose::Grid, fake_animation(&ctx, 64));
        anims.loaded("b".into(), Purpose::Grid, fake_animation(&ctx, 64));

        // Scroll: only "c" is on screen now. Real sizes are tiny, so force the
        // budget down to what is already held to exercise eviction.
        anims.budget_bytes = anims.grid_bytes;
        for _ in 0..3 {
            anims.begin_frame();
            anims.visible(&ids(&["c"]), 64);
        }
        anims.loaded("c".into(), Purpose::Grid, fake_animation(&ctx, 64));
        assert!(anims.grid_ids().contains("c"));
        assert!(
            anims.grid_bytes <= anims.budget_bytes,
            "off-screen previews must make way"
        );

        // Now everything visible already fills the budget: nothing is evicted
        // to fit more, the newcomer is simply not requested.
        let mut full = Animations::with_budget(0);
        full.begin_frame();
        full.visible(&ids(&["x"]), 64);
        full.loaded("x".into(), Purpose::Grid, fake_animation(&ctx, 64));
        full.begin_frame();
        assert!(
            full.visible(&ids(&["x", "y"]), 64).is_empty(),
            "with no room, keep what plays instead of thrashing"
        );
        assert!(full.grid_ids().contains("x"));
    }

    #[test]
    fn the_frame_an_animation_first_appears_in_already_asks_for_repaints() {
        let ctx = egui::Context::default();
        let mut anims = Animations::default();
        anims.begin_frame();
        anims.visible(&ids(&["a"]), 64);
        assert!(!anims.is_moving(), "nothing loaded yet, nothing moves");

        anims.loaded("a".into(), Purpose::Grid, fake_animation(&ctx, 64));
        anims.begin_frame();
        anims.visible(&ids(&["a"]), 64);
        assert!(
            anims.is_moving(),
            "the first frame showing it must schedule the next, or it stays frozen"
        );

        anims.begin_frame();
        anims.visible(&ids(&["b"]), 64);
        assert!(!anims.is_moving(), "scrolled away: stop repainting");
    }

    #[test]
    fn selected_panel_animation_counts_as_moving() {
        let ctx = egui::Context::default();
        let mut anims = Animations::default();
        assert!(anims.want_selected("s"));
        assert!(!anims.want_selected("s"), "already in flight");
        anims.loaded("s".into(), Purpose::Selected, fake_animation(&ctx, 256));
        anims.begin_frame();
        assert!(anims.selected_frame("s").is_some());
        assert!(anims.is_moving());
        assert!(anims.selected_frame("other").is_none());
        assert!(!anims.want_selected("s"), "already loaded");
    }
}
