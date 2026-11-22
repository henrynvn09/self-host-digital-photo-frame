package geo

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Location struct {
	City    string
	State   string
	Country string
}

type cityEntry struct {
	name      string
	admin1Key string
	country   string
	lat       float64
	lon       float64
}

type gridKey struct {
	latCell int
	lonCell int
}

// Resolver performs fast (<1ms) offline worldwide reverse geocoding using a
// 1-degree spatial grid over GeoNames cities1000 data.
type Resolver struct {
	grid      map[gridKey][]cityEntry
	admin1    map[string]string
	countries map[string]string
}

// LoadFromDir loads cities1000.txt, admin1CodesASCII.txt, and countryInfo.txt
// from dir. If cities1000.txt does not exist, it returns (nil, nil) so the
// service can still run cleanly without geocoding data.
func LoadFromDir(dir string) (*Resolver, error) {
	if dir == "" {
		return nil, nil
	}
	citiesPath := filepath.Join(dir, "cities1000.txt")
	if _, err := os.Stat(citiesPath); os.IsNotExist(err) {
		return nil, nil
	}

	r := &Resolver{
		grid:      make(map[gridKey][]cityEntry, 16384),
		admin1:    make(map[string]string, 4096),
		countries: make(map[string]string, 256),
	}

	_ = r.loadCountries(filepath.Join(dir, "countryInfo.txt"))
	_ = r.loadAdmin1(filepath.Join(dir, "admin1CodesASCII.txt"))

	if err := r.loadCities(citiesPath); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Resolver) loadCountries(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 5 {
			r.countries[fields[0]] = fields[4]
		}
	}
	return sc.Err()
}

func (r *Resolver) loadAdmin1(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 2 {
			r.admin1[fields[0]] = fields[1]
		}
	}
	return sc.Err()
}

func (r *Resolver) loadCities(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 11 {
			continue
		}
		lat, err1 := strconv.ParseFloat(fields[4], 64)
		lon, err2 := strconv.ParseFloat(fields[5], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		countryCode := fields[8]
		admin1Code := fields[10]
		entry := cityEntry{
			name:      fields[1],
			admin1Key: countryCode + "." + admin1Code,
			country:   countryCode,
			lat:       lat,
			lon:       lon,
		}
		k := cellFor(lat, lon)
		r.grid[k] = append(r.grid[k], entry)
	}
	return sc.Err()
}

func cellFor(lat, lon float64) gridKey {
	return gridKey{
		latCell: int(math.Floor(lat)),
		lonCell: int(math.Floor(lon)),
	}
}

// Lookup returns the nearest city within ~150km for (lat, lon).
func (r *Resolver) Lookup(lat, lon float64) (Location, bool) {
	if r == nil || len(r.grid) == 0 {
		return Location{}, false
	}
	center := cellFor(lat, lon)

	var best *cityEntry
	bestDist := math.MaxFloat64

	// Search 3x3 degree neighborhood (~111km+ radius)
	for dLat := -1; dLat <= 1; dLat++ {
		for dLon := -1; dLon <= 1; dLon++ {
			k := gridKey{latCell: center.latCell + dLat, lonCell: center.lonCell + dLon}
			for i := range r.grid[k] {
				c := &r.grid[k][i]
				d := approxDistKm(lat, lon, c.lat, c.lon)
				if d < bestDist {
					bestDist = d
					best = c
				}
			}
		}
	}

	if best == nil || bestDist > 150 {
		return Location{}, false
	}

	countryName := r.countries[best.country]
	if countryName == "" {
		countryName = best.country
	}
	return Location{
		City:    best.name,
		State:   r.admin1[best.admin1Key],
		Country: countryName,
	}, true
}

func approxDistKm(lat1, lon1, lat2, lon2 float64) float64 {
	const rad = math.Pi / 180
	dLat := (lat2 - lat1) * 111.0
	avgLat := (lat1 + lat2) * 0.5 * rad
	dLon := (lon2 - lon1) * 111.320 * math.Cos(avgLat)
	return math.Hypot(dLat, dLon)
}
