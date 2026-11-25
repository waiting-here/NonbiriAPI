package db

// CharityDiscountPrecisionSQL retains the exact price of a reservation across rule changes.
// Rows created by earlier versions continue to use their integer percent snapshot.
const CharityDiscountPrecisionSQL = `
ALTER TABLE charity_reservations ADD COLUMN discount_basis_points INTEGER
 CHECK(discount_basis_points IS NULL OR (typeof(discount_basis_points)='integer' AND discount_basis_points BETWEEN 0 AND 10000));
`
