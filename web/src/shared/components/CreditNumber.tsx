/** Display-only parts of an already formatted credit amount. */
export function CreditNumber({ value }: { readonly value: string }) {
  const point = value.indexOf('.');
  return (
    <span className="credit-number">
      {point < 0 ? value : value.slice(0, point)}
      {point < 0 ? null : <span className="credit-number__fraction">{value.slice(point)}</span>}
    </span>
  );
}
