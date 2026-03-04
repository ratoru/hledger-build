package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newMetricsCmd() *cobra.Command {
	var (
		flagFile            string
		flagYear            int
		flagFireFactor      int
		flagExcludeExpenses []string
		flagExcludeRevenue  []string
		flagCashAssets      string
		flagCurrency        string
		flagAge             int
	)
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Compute monthly personal finance metrics for a single year",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMetrics(flagFile, flagYear, flagFireFactor, flagExcludeExpenses, flagExcludeRevenue, flagCashAssets, flagCurrency, flagAge)
		},
		SilenceUsage: true,
	}
	cmd.Flags().StringVarP(&flagFile, "file", "f", "", "journal file (required)")
	cmd.Flags().IntVar(&flagYear, "year", 0, "year (required)")
	cmd.Flags().IntVar(&flagFireFactor, "fire-factor", 25, "FIRE multiplier")
	cmd.Flags().StringSliceVar(&flagExcludeExpenses, "exclude-expenses", []string{"expenses:gross"}, "expense accounts to exclude from daily avg (comma-separated)")
	cmd.Flags().StringSliceVar(&flagExcludeRevenue, "exclude-revenue", []string{"revenue:gift"}, "revenue accounts to exclude from daily avg (comma-separated)")
	cmd.Flags().StringVar(&flagCashAssets, "cash-assets", "assets:cash", "liquid cash account for short runway")
	cmd.Flags().StringVar(&flagCurrency, "currency", "", "target currency for --value=end (empty = native)")
	cmd.Flags().IntVar(&flagAge, "age", 0, "age for AAW/PAW thresholds (0 = skip)")
	_ = cmd.MarkFlagRequired("file")
	_ = cmd.MarkFlagRequired("year")
	return cmd
}

// runMetrics runs hledger queries, computes metrics, and writes the report to stdout.
func runMetrics(journalFile string, year, fireFactor int, excludeExpenses, excludeRevenue []string, cashAssets, currency string, age int) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	baseArgs := []string{
		"-f", journalFile,
		"-M",
		"-b", fmt.Sprintf("%d-01-01", year),
		"-e", fmt.Sprintf("%d-01-01", year+1),
		"-O", "csv",
		"-N",
	}
	if currency != "" {
		baseArgs = append(baseArgs, "--value=end,"+currency)
	}

	runQuery := func(extraArgs ...string) (string, error) {
		args := append(append([]string{}, baseArgs...), extraArgs...)
		cmd := exec.Command(cfg.HledgerBinary, args...)
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("hledger %v: %w", args, err)
		}
		return string(out), nil
	}

	// 1. Build and run Expenses query dynamically.
	expArgs := []string{"balance", "expenses"}
	for _, ex := range excludeExpenses {
		if ex != "" {
			expArgs = append(expArgs, "not:"+ex)
		}
	}
	expArgs = append(expArgs, "--depth", "1")
	expOut, err := runQuery(expArgs...)
	if err != nil {
		return err
	}

	// 2. Build and run Revenue query dynamically.
	incArgs := []string{"balance", "revenue"}
	for _, ex := range excludeRevenue {
		if ex != "" {
			incArgs = append(incArgs, "not:"+ex)
		}
	}
	incArgs = append(incArgs, "--depth", "1")
	incOut, err := runQuery(incArgs...)
	if err != nil {
		return err
	}

	// 3. Build and run Gross Deductions query dynamically.
	grossArgs := []string{"balance"}
	grossArgs = append(grossArgs, excludeExpenses...)
	grossOut, err := runQuery(grossArgs...)
	if err != nil {
		return err
	}
	assetsOut, err := runQuery("balance", "assets", "--historical", "--depth", "1")
	if err != nil {
		return err
	}
	liabOut, err := runQuery("balance", "liabilities", "--historical", "--depth", "1")
	if err != nil {
		return err
	}
	cashOut, err := runQuery("balance", cashAssets, "--historical")
	if err != nil {
		return err
	}

	// Parse all outputs.
	expenses, err := parseMultiperiodCSV(expOut)
	if err != nil {
		return fmt.Errorf("parsing expenses: %w", err)
	}
	revenue, err := parseMultiperiodCSV(incOut)
	if err != nil {
		return fmt.Errorf("parsing revenue: %w", err)
	}
	gross, err := parseMultiperiodCSV(grossOut)
	if err != nil {
		return fmt.Errorf("parsing gross: %w", err)
	}
	assets, err := parseMultiperiodCSV(assetsOut)
	if err != nil {
		return fmt.Errorf("parsing assets: %w", err)
	}
	liabilities, err := parseMultiperiodCSV(liabOut)
	if err != nil {
		return fmt.Errorf("parsing liabilities: %w", err)
	}
	cash, err := parseMultiperiodCSV(cashOut)
	if err != nil {
		return fmt.Errorf("parsing cash: %w", err)
	}

	currencyPrefix := currency

	fmtMoney := func(v float64) string {
		if currencyPrefix != "" {
			return fmt.Sprintf("%s%.0f", currencyPrefix, math.Round(v))
		}
		return fmt.Sprintf("%.2f", v)
	}

	// Header
	now := time.Now()
	fmt.Printf("Financial Metrics: %d\n", year)
	fmt.Printf("Generated: %s\n\n", now.Format("2006-01-02"))

	// Dynamically construct main table header and separator
	headerStr := fmt.Sprintf("%-10s %12s %12s %12s %10s %10s %10s %12s",
		"Month", "Daily Exp", "Daily Inc", "Net Worth", "Savings%", "Short Run", "Long Run", "FIRE#")

	sepStr := strings.Repeat("-", 10) + " " +
		strings.Repeat("-", 12) + " " +
		strings.Repeat("-", 12) + " " +
		strings.Repeat("-", 12) + " " +
		strings.Repeat("-", 10) + " " +
		strings.Repeat("-", 10) + " " +
		strings.Repeat("-", 10) + " " +
		strings.Repeat("-", 12)

	if age > 0 {
		headerStr += fmt.Sprintf(" %12s %12s", "Min AAW", "Min PAW")
		sepStr += " " + strings.Repeat("-", 14) + " " + strings.Repeat("-", 14)
	}

	fmt.Println(headerStr)
	fmt.Println(sepStr)

	// Yearly tracking variables
	var totalExp, totalInc, totalGross float64
	var endOfYearNetWorth, endOfYearCash float64

	for m := time.January; m <= time.December; m++ {
		period := fmt.Sprintf("%d-%02d", year, int(m))

		numDays := float64(daysInMonth(year, m))

		exp := expenses[period]
		inc := revenue[period]
		grs := gross[period]
		ast := assets[period]
		liab := liabilities[period]
		csh := cash[period]

		dailyExp := exp / numDays
		dailyInc := -inc / numDays
		netWorth := ast + liab
		annualInc := dailyInc * 365

		// Accumulate yearly totals
		totalExp += exp
		totalInc += -inc // inc is negative from hledger
		totalGross += grs
		endOfYearNetWorth = netWorth // will naturally hold December's value at the end
		endOfYearCash = csh

		// Savings rate: (take-home income - expenses) / take-home income × 100
		takeHome := -inc - grs
		var savingsStr string
		if takeHome != 0 {
			savings := (takeHome - exp) / takeHome * 100
			savingsStr = fmt.Sprintf("%.1f%%", savings)
		} else {
			savingsStr = "—"
		}

		// Runway and FIRE
		var shortRunStr, longRunStr, fireStr string
		if dailyExp > 0 {
			shortRun := csh / dailyExp
			longRun := ast / dailyExp
			fireNum := dailyExp * 365 * float64(fireFactor)
			shortRunStr = fmt.Sprintf("%.0fd", shortRun)
			longRunStr = fmt.Sprintf("%.0fd", longRun)
			fireStr = fmtMoney(fireNum)
		} else {
			shortRunStr = "—"
			longRunStr = "—"
			fireStr = "—"
		}

		// Construct the base row string
		rowStr := fmt.Sprintf("%-10s %12s %12s %12s %10s %10s %10s %12s",
			period,
			fmtMoney(dailyExp),
			fmtMoney(dailyInc),
			fmtMoney(netWorth),
			savingsStr,
			shortRunStr,
			longRunStr,
			fireStr,
		)

		// Append AAW/PAW columns if age is provided
		if age > 0 {
			aawThreshold := (annualInc * float64(age) / 10) / 2
			pawThreshold := (annualInc * float64(age) / 10) * 2
			rowStr += fmt.Sprintf(" %12s %12s", fmtMoney(aawThreshold), fmtMoney(pawThreshold))
		}

		fmt.Println(rowStr)
	}

	// Calculate and print the Yearly Average row
	daysInYear := 365.0
	if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
		daysInYear = 366.0 // Handle leap years
	}
	dailyExpYear := totalExp / daysInYear
	dailyIncYear := totalInc / daysInYear

	takeHomeYear := totalInc - totalGross
	var savingsStrYear string
	if takeHomeYear != 0 {
		savings := ((takeHomeYear - totalExp) / takeHomeYear) * 100
		savingsStrYear = fmt.Sprintf("%.1f%%", savings)
	} else {
		savingsStrYear = "—"
	}

	var shortRunYear, longRunYear, fireYear string
	if dailyExpYear > 0 {
		shortRunYear = fmt.Sprintf("%.0fd", endOfYearCash/dailyExpYear)
		longRunYear = fmt.Sprintf("%.0fd", endOfYearNetWorth/dailyExpYear)
		fireYear = fmtMoney(dailyExpYear * 365 * float64(fireFactor))
	} else {
		shortRunYear, longRunYear, fireYear = "—", "—", "—"
	}

	fmt.Println(sepStr) // Print a divider before the total row

	rowStrYear := fmt.Sprintf("%-10s %12s %12s %12s %10s %10s %10s %12s",
		"Year Avg",
		fmtMoney(dailyExpYear),
		fmtMoney(dailyIncYear),
		fmtMoney(endOfYearNetWorth), // Shows ending net worth for the year
		savingsStrYear,
		shortRunYear,
		longRunYear,
		fireYear,
	)

	if age > 0 {
		// Calculate the TRUE AAW/PAW based on total actual yearly income
		aawYear := (totalInc * float64(age) / 10) / 2
		pawYear := (totalInc * float64(age) / 10) * 2
		rowStrYear += fmt.Sprintf(" %12s %12s", fmtMoney(aawYear), fmtMoney(pawYear))
	}

	fmt.Println(rowStrYear)

	fmt.Printf(`
Notes:
  Daily Exp/Inc: average for the month/year, excl. pay deductions/gifts
  Net Worth:     assets minus liabilities at end of period
  Savings%%:      (take-home income - expenses) / take-home income
  Short Runway:  liquid cash / daily expense
  Long Runway:   total assets / daily expense
  FIRE#:         %dx annual expenses (Financial Independence, Retire Early)
`, fireFactor)
	if age > 0 {
		fmt.Printf(`  AAW/PAW:       Target net worth benchmarks (Average/Prodigious Accumulator of Wealth) at age %d
`, age)
	}

	return nil
}

// digitsRe matches the unsigned numeric part of an hledger amount.
var digitsRe = regexp.MustCompile(`[\d,]+\.?\d*`)

// parseAmount parses an hledger amount string such as "£100.00", "100.00 GBP",
// "-50.00", "-£1,234.56", or "0" into a float64. The sign may appear before
// a currency symbol (e.g. "-£50").
func parseAmount(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	negative := strings.Contains(s, "-")

	m := digitsRe.FindString(s)
	if m == "" {
		return 0, fmt.Errorf("no numeric value found in %q", s)
	}
	// Remove thousands separators.
	m = strings.ReplaceAll(m, ",", "")
	v, err := strconv.ParseFloat(m, 64)
	if err != nil {
		return 0, err
	}
	if negative {
		v = -v
	}
	return v, nil
}

// parseMultiperiodCSV parses hledger -M -O csv -N output.
func parseMultiperiodCSV(data string) (map[string]float64, error) {
	data = strings.TrimSpace(data)
	result := make(map[string]float64)
	if data == "" {
		return result, nil
	}

	r := csv.NewReader(strings.NewReader(data))
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing CSV: %w", err)
	}
	if len(records) == 0 {
		return result, nil
	}

	// First row is the header: "account", "period1", "period2", ...
	header := records[0]
	if len(header) < 2 {
		return result, nil
	}
	// Columns 1+ are period labels like "2026-01-01..2026-02-01" or "2026-01".
	// Normalise to "YYYY-MM".
	periods := make([]string, len(header)-1)
	for i, h := range header[1:] {
		periods[i] = normalisePeriod(h)
	}

	// Data rows: sum all account values per period column.
	for _, row := range records[1:] {
		if len(row) < 2 {
			continue
		}
		for col, periodKey := range periods {
			cellIdx := col + 1
			if cellIdx >= len(row) {
				continue
			}
			v, err := parseAmount(row[cellIdx])
			if err != nil {
				// Ignore unparseable cells (e.g. empty or "0")
				continue
			}
			result[periodKey] += v
		}
	}

	return result, nil
}

// normalisePeriod converts hledger period labels to "YYYY-MM".
var yyyymmRe = regexp.MustCompile(`(\d{4})-(\d{2})`)

func normalisePeriod(s string) string {
	m := yyyymmRe.FindString(s)
	if m != "" {
		return m
	}
	return s
}

// daysInMonth returns the number of days in the given month of the given year.
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
