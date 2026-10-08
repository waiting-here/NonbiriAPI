import { MarkdownText } from '@shared/components/MarkdownText';
export function LegalSections({ override }: { override: string }) {
  return (
    <MarkdownText className="legal-markdown" headingShift={0}>
      {override}
    </MarkdownText>
  );
}
