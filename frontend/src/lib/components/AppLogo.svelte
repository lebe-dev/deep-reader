<script lang="ts">
	// The app mark: a rounded square filled to 80% with the primary colour, its
	// surface rippling gently and the occasional bubble rising from the bottom —
	// "deep" reading as water in a vessel. The initials sit submerged in it.
	//
	// It is one inline SVG rather than a stack of divs so the rounded corners clip
	// the waves and bubbles exactly, and so it scales with `size` without
	// recomputing radii. The motion is decorative, so the whole thing is
	// aria-hidden and honours prefers-reduced-motion by holding still.

	import { cn } from '$lib/utils';
	import { deviceTilt, startDeviceTilt } from '$lib/device-tilt.svelte';

	interface Props {
		/** Tailwind size class for the square. */
		class?: string;
	}

	let { class: className }: Props = $props();

	// On a phone the water answers the gyroscope: tilt the device and the surface
	// stays level, as liquid in a glass would. Counter-rotating by the roll is
	// what produces that. It is a no-op on anything without the sensor.
	$effect(() => startDeviceTilt());

	// Water surface height: 80% of the box, so the top edge sits at y = 4.8 in the
	// 24-unit viewBox. The waves oscillate around that line.
	const SURFACE_Y = 4.8;

	// One wave period is 24 units wide, and each path is drawn eight periods wide
	// starting well LEFT of the box and running well BELOW it. The extra margin is
	// what lets the water rotate with the device without a corner of the vessel
	// showing through as empty. Translating by exactly -24 still loops seamlessly.
	const wavePath = (y: number, amplitude: number) =>
		`M-48,${y} q6,${-amplitude} 12,0 t12,0 t12,0 t12,0 t12,0 t12,0 t12,0 t12,0 ` +
		`t12,0 t12,0 t12,0 t12,0 t12,0 t12,0 t12,0 t12,0 V72 H-48 Z`;

	// Bubbles: hand-placed rather than random so the mark renders identically
	// everywhere (a logo that differs per reload is not a logo). The varied radii,
	// durations and delays are what make the rising read as chaotic; the long
	// cycles with a mostly-invisible phase are what make bubbles RARE — usually
	// none, occasionally one or two.
	const bubbles = [
		{ cx: 7.5, r: 0.8, duration: 22.5, delay: 0 },
		{ cx: 15.5, r: 0.6, duration: 28.5, delay: 7.8 },
		{ cx: 11, r: 0.5, duration: 25.5, delay: 15.3 },
		{ cx: 18.5, r: 0.75, duration: 33, delay: 22.2 },
		{ cx: 5, r: 0.45, duration: 30, delay: 11.4 }
	];
</script>

<span class={cn('inline-flex shrink-0', className)} aria-hidden="true">
	<svg viewBox="0 0 24 24" class="size-full overflow-hidden rounded-[28%]" role="presentation">
		<!-- The vessel: a faint tint so the empty 20% reads as air, not a gap. -->
		<rect x="0" y="0" width="24" height="24" rx="6.7" class="fill-primary/15" />

		<!-- Everything below is clipped to the rounded square. -->
		<clipPath id="app-logo-clip">
			<rect x="0" y="0" width="24" height="24" rx="6.7" />
		</clipPath>

		<g clip-path="url(#app-logo-clip)">
			<!-- The water — waves and bubbles together — tilts as one body. The
				 letters are deliberately OUTSIDE this group: they are the mark's
				 identity, not part of the liquid, so they must never move. -->
			<g class="water" style="transform: rotate({-deviceTilt.roll}deg)">
				<!-- Back swell: slower and fainter, so the surface has depth rather
					 than looking like a single sliding shape. -->
				<path d={wavePath(SURFACE_Y + 0.5, 1.6)} class="fill-primary/60 wave wave-back" />
				<!-- Front swell: the actual water line. -->
				<path d={wavePath(SURFACE_Y, 1.2)} class="fill-primary wave wave-front" />

				<!-- Bubbles rise INSIDE the water, so they are drawn after the swells
					 and stop short of the surface line. They tilt with it, because a
					 bubble rises perpendicular to the surface, not to the screen. -->
				{#each bubbles as bubble, i (i)}
					<circle
						cx={bubble.cx}
						cy="22.5"
						r={bubble.r}
						class="bubble"
						style="animation-duration: {bubble.duration}s; animation-delay: -{bubble.delay}s"
					/>
				{/each}
			</g>

			<!-- The initials, submerged: drawn after the water so they are not
				 painted over, but in a pale blue rather than white — white would
				 read as sitting ON the surface, and these are meant to be under it.
				 The slight transparency lets the water colour show through. -->
			<text x="12" y="15.2" text-anchor="middle" dominant-baseline="middle" class="initials"
				>DR</text
			>
		</g>
	</svg>
</span>

<style>
	.water {
		/* Rotate about the middle of the vessel. transform-box: view-box is what
		   makes transform-origin resolve in viewBox units rather than against the
		   group's own (much wider) bounding box. */
		transform-box: view-box;
		transform-origin: 12px 12px;
		/* Follow the sensor with a short lag, so the surface settles like liquid
		   instead of snapping to every reading. */
		transition: transform 350ms cubic-bezier(0.22, 1, 0.36, 1);
		will-change: transform;
	}

	.wave {
		/* Each path is four 24-unit periods wide, so a -24 shift lands on an
		   identical crest — the loop has no seam to hide. */
		animation: wave-drift linear infinite;
		will-change: transform;
	}

	.wave-front {
		animation-duration: 4.5s;
	}

	/* Offset and slower, so the two crests drift apart instead of moving as one. */
	.wave-back {
		animation-duration: 7s;
		animation-direction: reverse;
	}

	@keyframes wave-drift {
		from {
			transform: translateX(0);
		}
		to {
			transform: translateX(-24px);
		}
	}

	.initials {
		/* A desaturated, slightly darkened sky blue: light enough to read against
		   the primary fill, but never white — submerged type is tinted by the
		   water above it. */
		fill: #b8e2f5;
		fill-opacity: 0.82;
		font-family: inherit;
		font-size: 9px;
		font-weight: 700;
		letter-spacing: -0.4px;
		/* Selecting the letters of a logo is never what the user meant. */
		user-select: none;
	}

	.bubble {
		fill: #fff;
		animation-name: bubble-rise;
		animation-timing-function: ease-in;
		animation-iteration-count: infinite;
		will-change: transform, opacity;
	}

	/* A bubble is invisible for the first 85% of its cycle, so at any moment most
	   of them are simply not there. That is what "rare" means here — the
	   alternative, every bubble always mid-flight, reads as a fizzing drink.
	   The rise itself keeps its original ~3.5s: the cycles were tripled and this
	   phase shrunk to a third to match, so bubbles appear three times as seldom
	   WITHOUT drifting up in slow motion. */
	@keyframes bubble-rise {
		0%,
		85% {
			transform: translate(0, 0);
			opacity: 0;
		}
		87.33% {
			opacity: 0.75;
		}
		/* Slight sideways wobble on the way up, so the path is not a ruled line. */
		92.67% {
			transform: translate(0.8px, -8px);
			opacity: 0.7;
		}
		97.33% {
			transform: translate(-0.5px, -14px);
			opacity: 0.45;
		}
		/* Fades out just under the surface rather than popping through it. */
		100% {
			transform: translate(0.2px, -16.5px);
			opacity: 0;
		}
	}

	/* Decorative motion: hold still when the user asks for less of it. */
	@media (prefers-reduced-motion: reduce) {
		.water {
			/* Rotate about the middle of the vessel. transform-box: view-box is what
		   makes transform-origin resolve in viewBox units rather than against the
		   group's own (much wider) bounding box. */
			transform-box: view-box;
			transform-origin: 12px 12px;
			/* Follow the sensor with a short lag, so the surface settles like liquid
		   instead of snapping to every reading. */
			transition: transform 350ms cubic-bezier(0.22, 1, 0.36, 1);
			will-change: transform;
		}

		.wave {
			animation: none;
		}

		.bubble {
			display: none;
		}
	}
</style>
