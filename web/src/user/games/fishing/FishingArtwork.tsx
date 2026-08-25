import { useId, type ReactNode } from 'react';
import {
  resolveFishingArtwork,
  type FinStyle,
  type FishArtDescriptor,
  type FishFamily,
  type FishPattern,
  type FishingArtDescriptor,
} from './artRegistry';
import './fishing-art.css';

export interface FishingArtworkProps {
  itemKey: string;
  label: string;
  decorative?: boolean;
  showLabel?: boolean;
  className?: string;
}

function FishSilhouette({ family }: { family: FishFamily }) {
  switch (family) {
    case 'minnow':
      return (
        <>
          <path className="fishing-art__tail" d="M37 48 16 31v34Z" />
          <path className="fishing-art__body" d="M32 48c17-20 65-21 91-2-25 23-73 24-91 2Z" />
          <path className="fishing-art__mouth" d="m122 46 10 3-10 4" />
        </>
      );
    case 'eel':
      return (
        <>
          <path className="fishing-art__tail" d="M31 60 15 71l8-23Z" />
          <path
            className="fishing-art__body fishing-art__body--eel"
            d="M24 58c25-34 54 17 78-15 11-14 23-12 35-3-8 21-27 22-42 15-28-13-39 20-71 3Z"
          />
          <path className="fishing-art__mouth" d="m136 40 8 4-9 3" />
        </>
      );
    case 'panfish':
      return (
        <>
          <path className="fishing-art__tail" d="M38 49 17 29v40Z" />
          <path className="fishing-art__body" d="M34 49c13-31 68-38 94-2-20 34-75 35-94 2Z" />
          <path className="fishing-art__mouth" d="m127 46 10 4-10 4" />
        </>
      );
    case 'catfish':
      return (
        <>
          <path className="fishing-art__tail" d="M38 51 17 34l3 35Z" />
          <path
            className="fishing-art__body"
            d="M34 51c18-26 70-28 98-7 7 8 1 19-12 21-35 6-68 4-86-14Z"
          />
          <path className="fishing-art__mouth" d="M127 55q9 6 15-1" />
        </>
      );
    case 'carp':
      return (
        <>
          <path className="fishing-art__tail" d="M39 49 14 27l7 22-7 21Z" />
          <path
            className="fishing-art__body"
            d="M35 49c16-27 71-32 100-5 5 7 2 14-5 19-29 21-79 13-95-14Z"
          />
          <path className="fishing-art__mouth" d="m133 45 10 4-10 5" />
        </>
      );
    case 'predator':
      return (
        <>
          <path className="fishing-art__tail" d="M34 49 12 30l8 20-8 18Z" />
          <path
            className="fishing-art__body"
            d="M29 49c24-22 70-23 111-4l8 6-10 8c-42 14-84 11-109-10Z"
          />
          <path className="fishing-art__mouth" d="m138 51 10 0" />
        </>
      );
    case 'salmonid':
      return (
        <>
          <path className="fishing-art__tail" d="M37 49 13 27l7 22-7 22Z" />
          <path className="fishing-art__body" d="M32 49c23-29 76-27 108-2-29 31-83 31-108 2Z" />
          <path className="fishing-art__adipose" d="m94 29 10-7 10 11" />
          <path className="fishing-art__mouth" d="m138 47 9 3-9 4" />
        </>
      );
  }
}

function FishPatternDetail({ pattern }: { pattern: FishPattern }) {
  switch (pattern) {
    case 'plain':
      return null;
    case 'stripe':
      return (
        <g className="fishing-art__pattern">
          <path d="m59 34 5 29" />
          <path d="m76 31 5 34" />
          <path d="m94 32 4 31" />
        </g>
      );
    case 'spot':
      return (
        <g className="fishing-art__pattern fishing-art__pattern--fill">
          <circle cx="62" cy="43" r="4" />
          <circle cx="79" cy="55" r="3" />
          <circle cx="96" cy="40" r="3.5" />
        </g>
      );
    case 'band':
      return (
        <path
          className="fishing-art__pattern fishing-art__pattern--band"
          d="M70 30q-8 18 1 36h14q-9-19 0-37Z"
        />
      );
    case 'belly':
      return (
        <path
          className="fishing-art__pattern fishing-art__pattern--belly"
          d="M43 55q36 19 77 5-26 20-65 9Z"
        />
      );
    case 'scale':
      return (
        <g className="fishing-art__pattern">
          <path d="M55 41q8 9 16 0M70 41q8 9 16 0M85 41q8 9 16 0" />
          <path d="M62 51q8 9 16 0M77 51q8 9 16 0M92 51q8 9 16 0" />
        </g>
      );
    case 'saddle':
      return (
        <path
          className="fishing-art__pattern fishing-art__pattern--saddle"
          d="m57 32 14-5 11 40-18 0Z"
        />
      );
    case 'patch':
      return (
        <g className="fishing-art__pattern fishing-art__pattern--fill">
          <ellipse cx="65" cy="42" rx="12" ry="8" />
          <ellipse cx="96" cy="55" rx="9" ry="6" />
        </g>
      );
  }
}

function FinDetail({ style }: { style: FinStyle }) {
  const paths: Record<FinStyle, string> = {
    soft: 'M74 31q9-12 20 0M76 66q8 9 18 0',
    round: 'M67 31q12-18 27 1M72 66q11 14 24-1',
    forked: 'm69 32 9-16 8 15M76 65l8 16 10-17',
    swept: 'm66 33 23-19 8 19M76 64l24 15-8-17',
    spined: 'm58 35 8-16 7 13 8-17 8 17 9-12 5 16',
  };
  return <path className={`fishing-art__fin fishing-art__fin--${style}`} d={paths[style]} />;
}

function FishArtworkShape({ descriptor }: { descriptor: FishArtDescriptor }) {
  return (
    <g className="fishing-art__fish">
      <FishSilhouette family={descriptor.family} />
      <FishPatternDetail pattern={descriptor.pattern} />
      <FinDetail style={descriptor.fin} />
      <circle className="fishing-art__eye" cx="120" cy="43" r="3.5" />
      <circle className="fishing-art__eye-glint" cx="121" cy="42" r="1" />
      {descriptor.barbels ? (
        <g className="fishing-art__barbels">
          <path d="M129 54q13 8 19 2" />
          <path d="M128 57q9 13 18 12" />
        </g>
      ) : null}
    </g>
  );
}

function JunkArtworkShape({ itemKey }: { itemKey: string }) {
  switch (itemKey) {
    case 'boot':
      return (
        <path className="fishing-art__object" d="M62 17h27v40q8 12 32 14 9 1 9 10H49V67h13Z" />
      );
    case 'seaweed':
      return (
        <g className="fishing-art__object fishing-art__object--seaweed">
          <path d="M80 82q-22-23-4-64M81 80q25-22 8-61M80 76q-3-29 19-42M75 61Q54 49 58 31" />
          <path d="M53 82h56" />
        </g>
      );
    case 'plastic_bag':
      return (
        <g className="fishing-art__object">
          <path d="M48 34h64l8 49H40Z" />
          <path d="M61 37q1-24 19-24t19 24" />
          <path d="m50 55 11-7M108 55l-11-7" />
        </g>
      );
    case 'branch':
      return (
        <g className="fishing-art__object fishing-art__object--branch">
          <path d="M28 74 132 26M70 55 52 26M90 46l24 26M103 39l11-19" />
          <circle cx="28" cy="74" r="5" />
        </g>
      );
    case 'old_tire':
      return (
        <g className="fishing-art__object fishing-art__object--tire">
          <circle cx="80" cy="49" r="35" />
          <circle cx="80" cy="49" r="17" />
          <path d="M56 23 68 34M92 64l12 12M104 23 93 35M67 64 56 76" />
        </g>
      );
    case 'glasses':
      return (
        <g className="fishing-art__object fishing-art__object--glasses">
          <circle cx="55" cy="51" r="22" />
          <circle cx="105" cy="51" r="22" />
          <path d="M77 49q4-6 8 0M33 43 17 35M127 43l16-8" />
        </g>
      );
    case 'phone_case':
      return (
        <g className="fishing-art__object">
          <rect x="52" y="10" width="56" height="76" rx="12" />
          <rect x="61" y="19" width="20" height="15" rx="5" />
          <circle cx="67" cy="25" r="3" />
          <circle cx="76" cy="25" r="3" />
          <path d="M73 76h14" />
        </g>
      );
    case 'fry':
      return (
        <g className="fishing-art__object fishing-art__object--fry">
          <path d="M32 35q19-14 37 0-18 14-37 0Zm-1 0-12-8v16Zm59 23q24-18 47 0-23 18-47 0Zm-1 0-15-10v20Z" />
          <circle cx="59" cy="33" r="2" />
          <circle cx="126" cy="56" r="2" />
        </g>
      );
    default:
      return <UnknownArtworkShape />;
  }
}

function TreasureArtworkShape({ itemKey }: { itemKey: string }) {
  switch (itemKey) {
    case 'bottle':
      return (
        <g className="fishing-art__object fishing-art__object--treasure">
          <path d="M65 10h30v17l9 12v42H56V39l9-12Z" />
          <path d="M63 50q18-13 34 0v20q-17-11-34 0Z" />
          <path d="M66 17h28" />
        </g>
      );
    case 'clover':
      return (
        <g className="fishing-art__object fishing-art__object--treasure">
          <path d="M80 50Q42 45 48 24q6-18 32 9 26-27 32-9 6 21-32 26Z" />
          <path d="M80 49Q42 55 50 76q8 18 30-10 22 28 30 10 8-21-30-27Z" />
          <path d="M80 57q4 17-8 29" />
        </g>
      );
    case 'shell':
      return (
        <g className="fishing-art__object fishing-art__object--treasure">
          <path d="M35 70q4-49 45-56 41 7 45 56-36 25-90 0Z" />
          <path d="M80 17v65M80 18 57 78M80 18l23 60M80 18 44 69M80 18l36 51" />
        </g>
      );
    default:
      return <UnknownArtworkShape />;
  }
}

function UnknownArtworkShape() {
  return (
    <g className="fishing-art__object fishing-art__object--unknown">
      <rect x="38" y="12" width="84" height="72" rx="20" />
      <path d="M61 38q5-17 20-17 18 0 18 14 0 10-11 15-8 4-8 12" />
      <circle cx="80" cy="72" r="3" />
    </g>
  );
}

function artworkShape(descriptor: FishingArtDescriptor): ReactNode {
  if (descriptor.kind === 'fish') {
    return <FishArtworkShape descriptor={descriptor} />;
  }
  if (descriptor.kind === 'junk') {
    return <JunkArtworkShape itemKey={descriptor.key} />;
  }
  if (descriptor.kind === 'treasure') {
    return <TreasureArtworkShape itemKey={descriptor.key} />;
  }
  return <UnknownArtworkShape />;
}

export function FishingArtwork({
  itemKey,
  label,
  decorative = false,
  showLabel = true,
  className,
}: FishingArtworkProps) {
  const descriptor = resolveFishingArtwork(itemKey);
  const captionId = useId();
  const classes = [
    'fishing-art',
    `fishing-art--${descriptor.kind}`,
    descriptor.kind === 'fish' ? `fishing-art--${descriptor.palette}` : '',
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ');
  const caption =
    showLabel && !decorative ? (
      <figcaption id={captionId} className="fishing-art__label">
        {label}
      </figcaption>
    ) : null;

  return (
    <figure
      className={classes}
      data-art-key={descriptor.key}
      data-art-kind={descriptor.kind}
      data-art-variant={descriptor.kind === 'fish' ? descriptor.family : descriptor.key}
      role={decorative ? undefined : 'img'}
      aria-hidden={decorative || undefined}
      aria-label={!decorative && !caption ? label : undefined}
      aria-labelledby={!decorative && caption ? captionId : undefined}
    >
      <svg
        className="fishing-art__canvas"
        viewBox="0 0 160 96"
        preserveAspectRatio="xMidYMid meet"
        focusable="false"
        aria-hidden="true"
      >
        <path className="fishing-art__waterline" d="M14 82q22-7 44 0t44 0 44 0" />
        {artworkShape(descriptor)}
      </svg>
      {caption}
    </figure>
  );
}
