/*
 * Copyright (c) 2000-2018, 达梦数据库有限公司.
 * All rights reserved.
 */
package dm

import (
	"bytes"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/transform"
	"io"
	"io/ioutil"
	"math"
)

type dm_build_1345 struct{}

var Dm_build_1346 = &dm_build_1345{}

func (Dm_build_1348 *dm_build_1345) Dm_build_1347(dm_build_1349 []byte, dm_build_1350 int, dm_build_1351 byte) int {
	dm_build_1349[dm_build_1350] = dm_build_1351
	return 1
}

func (Dm_build_1353 *dm_build_1345) Dm_build_1352(dm_build_1354 []byte, dm_build_1355 int, dm_build_1356 int8) int {
	dm_build_1354[dm_build_1355] = byte(dm_build_1356)
	return 1
}

func (Dm_build_1358 *dm_build_1345) Dm_build_1357(dm_build_1359 []byte, dm_build_1360 int, dm_build_1361 int16) int {
	dm_build_1359[dm_build_1360] = byte(dm_build_1361)
	dm_build_1360++
	dm_build_1359[dm_build_1360] = byte(dm_build_1361 >> 8)
	return 2
}

func (Dm_build_1363 *dm_build_1345) Dm_build_1362(dm_build_1364 []byte, dm_build_1365 int, dm_build_1366 int32) int {
	dm_build_1364[dm_build_1365] = byte(dm_build_1366)
	dm_build_1365++
	dm_build_1364[dm_build_1365] = byte(dm_build_1366 >> 8)
	dm_build_1365++
	dm_build_1364[dm_build_1365] = byte(dm_build_1366 >> 16)
	dm_build_1365++
	dm_build_1364[dm_build_1365] = byte(dm_build_1366 >> 24)
	dm_build_1365++
	return 4
}

func (Dm_build_1368 *dm_build_1345) Dm_build_1367(dm_build_1369 []byte, dm_build_1370 int, dm_build_1371 int64) int {
	dm_build_1369[dm_build_1370] = byte(dm_build_1371)
	dm_build_1370++
	dm_build_1369[dm_build_1370] = byte(dm_build_1371 >> 8)
	dm_build_1370++
	dm_build_1369[dm_build_1370] = byte(dm_build_1371 >> 16)
	dm_build_1370++
	dm_build_1369[dm_build_1370] = byte(dm_build_1371 >> 24)
	dm_build_1370++
	dm_build_1369[dm_build_1370] = byte(dm_build_1371 >> 32)
	dm_build_1370++
	dm_build_1369[dm_build_1370] = byte(dm_build_1371 >> 40)
	dm_build_1370++
	dm_build_1369[dm_build_1370] = byte(dm_build_1371 >> 48)
	dm_build_1370++
	dm_build_1369[dm_build_1370] = byte(dm_build_1371 >> 56)
	return 8
}

func (Dm_build_1373 *dm_build_1345) Dm_build_1372(dm_build_1374 []byte, dm_build_1375 int, dm_build_1376 float32) int {
	return Dm_build_1373.Dm_build_1392(dm_build_1374, dm_build_1375, math.Float32bits(dm_build_1376))
}

func (Dm_build_1378 *dm_build_1345) Dm_build_1377(dm_build_1379 []byte, dm_build_1380 int, dm_build_1381 float64) int {
	return Dm_build_1378.Dm_build_1397(dm_build_1379, dm_build_1380, math.Float64bits(dm_build_1381))
}

func (Dm_build_1383 *dm_build_1345) Dm_build_1382(dm_build_1384 []byte, dm_build_1385 int, dm_build_1386 uint8) int {
	dm_build_1384[dm_build_1385] = byte(dm_build_1386)
	return 1
}

func (Dm_build_1388 *dm_build_1345) Dm_build_1387(dm_build_1389 []byte, dm_build_1390 int, dm_build_1391 uint16) int {
	dm_build_1389[dm_build_1390] = byte(dm_build_1391)
	dm_build_1390++
	dm_build_1389[dm_build_1390] = byte(dm_build_1391 >> 8)
	return 2
}

func (Dm_build_1393 *dm_build_1345) Dm_build_1392(dm_build_1394 []byte, dm_build_1395 int, dm_build_1396 uint32) int {
	dm_build_1394[dm_build_1395] = byte(dm_build_1396)
	dm_build_1395++
	dm_build_1394[dm_build_1395] = byte(dm_build_1396 >> 8)
	dm_build_1395++
	dm_build_1394[dm_build_1395] = byte(dm_build_1396 >> 16)
	dm_build_1395++
	dm_build_1394[dm_build_1395] = byte(dm_build_1396 >> 24)
	return 3
}

func (Dm_build_1398 *dm_build_1345) Dm_build_1397(dm_build_1399 []byte, dm_build_1400 int, dm_build_1401 uint64) int {
	dm_build_1399[dm_build_1400] = byte(dm_build_1401)
	dm_build_1400++
	dm_build_1399[dm_build_1400] = byte(dm_build_1401 >> 8)
	dm_build_1400++
	dm_build_1399[dm_build_1400] = byte(dm_build_1401 >> 16)
	dm_build_1400++
	dm_build_1399[dm_build_1400] = byte(dm_build_1401 >> 24)
	dm_build_1400++
	dm_build_1399[dm_build_1400] = byte(dm_build_1401 >> 32)
	dm_build_1400++
	dm_build_1399[dm_build_1400] = byte(dm_build_1401 >> 40)
	dm_build_1400++
	dm_build_1399[dm_build_1400] = byte(dm_build_1401 >> 48)
	dm_build_1400++
	dm_build_1399[dm_build_1400] = byte(dm_build_1401 >> 56)
	return 3
}

func (Dm_build_1403 *dm_build_1345) Dm_build_1402(dm_build_1404 []byte, dm_build_1405 int, dm_build_1406 []byte, dm_build_1407 int, dm_build_1408 int) int {
	copy(dm_build_1404[dm_build_1405:dm_build_1405+dm_build_1408], dm_build_1406[dm_build_1407:dm_build_1407+dm_build_1408])
	return dm_build_1408
}

func (Dm_build_1410 *dm_build_1345) Dm_build_1409(dm_build_1411 []byte, dm_build_1412 int, dm_build_1413 []byte, dm_build_1414 int, dm_build_1415 int) int {
	dm_build_1412 += Dm_build_1410.Dm_build_1392(dm_build_1411, dm_build_1412, uint32(dm_build_1415))
	return 4 + Dm_build_1410.Dm_build_1402(dm_build_1411, dm_build_1412, dm_build_1413, dm_build_1414, dm_build_1415)
}

func (Dm_build_1417 *dm_build_1345) Dm_build_1416(dm_build_1418 []byte, dm_build_1419 int, dm_build_1420 []byte, dm_build_1421 int, dm_build_1422 int) int {
	dm_build_1419 += Dm_build_1417.Dm_build_1387(dm_build_1418, dm_build_1419, uint16(dm_build_1422))
	return 2 + Dm_build_1417.Dm_build_1402(dm_build_1418, dm_build_1419, dm_build_1420, dm_build_1421, dm_build_1422)
}

func (Dm_build_1424 *dm_build_1345) Dm_build_1423(dm_build_1425 []byte, dm_build_1426 int, dm_build_1427 string, dm_build_1428 string, dm_build_1429 *DmConnection) int {
	dm_build_1430 := Dm_build_1424.Dm_build_1562(dm_build_1427, dm_build_1428, dm_build_1429)
	dm_build_1426 += Dm_build_1424.Dm_build_1392(dm_build_1425, dm_build_1426, uint32(len(dm_build_1430)))
	return 4 + Dm_build_1424.Dm_build_1402(dm_build_1425, dm_build_1426, dm_build_1430, 0, len(dm_build_1430))
}

func (Dm_build_1432 *dm_build_1345) Dm_build_1431(dm_build_1433 []byte, dm_build_1434 int, dm_build_1435 string, dm_build_1436 string, dm_build_1437 *DmConnection) int {
	dm_build_1438 := Dm_build_1432.Dm_build_1562(dm_build_1435, dm_build_1436, dm_build_1437)

	dm_build_1434 += Dm_build_1432.Dm_build_1387(dm_build_1433, dm_build_1434, uint16(len(dm_build_1438)))
	return 2 + Dm_build_1432.Dm_build_1402(dm_build_1433, dm_build_1434, dm_build_1438, 0, len(dm_build_1438))
}

func (Dm_build_1440 *dm_build_1345) Dm_build_1439(dm_build_1441 []byte, dm_build_1442 int) byte {
	return dm_build_1441[dm_build_1442]
}

func (Dm_build_1444 *dm_build_1345) Dm_build_1443(dm_build_1445 []byte, dm_build_1446 int) int16 {
	var dm_build_1447 int16
	dm_build_1447 = int16(dm_build_1445[dm_build_1446] & 0xff)
	dm_build_1446++
	dm_build_1447 |= int16(dm_build_1445[dm_build_1446]&0xff) << 8
	return dm_build_1447
}

func (Dm_build_1449 *dm_build_1345) Dm_build_1448(dm_build_1450 []byte, dm_build_1451 int) int32 {
	var dm_build_1452 int32
	dm_build_1452 = int32(dm_build_1450[dm_build_1451] & 0xff)
	dm_build_1451++
	dm_build_1452 |= int32(dm_build_1450[dm_build_1451]&0xff) << 8
	dm_build_1451++
	dm_build_1452 |= int32(dm_build_1450[dm_build_1451]&0xff) << 16
	dm_build_1451++
	dm_build_1452 |= int32(dm_build_1450[dm_build_1451]&0xff) << 24
	return dm_build_1452
}

func (Dm_build_1454 *dm_build_1345) Dm_build_1453(dm_build_1455 []byte, dm_build_1456 int) int64 {
	var dm_build_1457 int64
	dm_build_1457 = int64(dm_build_1455[dm_build_1456] & 0xff)
	dm_build_1456++
	dm_build_1457 |= int64(dm_build_1455[dm_build_1456]&0xff) << 8
	dm_build_1456++
	dm_build_1457 |= int64(dm_build_1455[dm_build_1456]&0xff) << 16
	dm_build_1456++
	dm_build_1457 |= int64(dm_build_1455[dm_build_1456]&0xff) << 24
	dm_build_1456++
	dm_build_1457 |= int64(dm_build_1455[dm_build_1456]&0xff) << 32
	dm_build_1456++
	dm_build_1457 |= int64(dm_build_1455[dm_build_1456]&0xff) << 40
	dm_build_1456++
	dm_build_1457 |= int64(dm_build_1455[dm_build_1456]&0xff) << 48
	dm_build_1456++
	dm_build_1457 |= int64(dm_build_1455[dm_build_1456]&0xff) << 56
	return dm_build_1457
}

func (Dm_build_1459 *dm_build_1345) Dm_build_1458(dm_build_1460 []byte, dm_build_1461 int) float32 {
	return math.Float32frombits(Dm_build_1459.Dm_build_1475(dm_build_1460, dm_build_1461))
}

func (Dm_build_1463 *dm_build_1345) Dm_build_1462(dm_build_1464 []byte, dm_build_1465 int) float64 {
	return math.Float64frombits(Dm_build_1463.Dm_build_1480(dm_build_1464, dm_build_1465))
}

func (Dm_build_1467 *dm_build_1345) Dm_build_1466(dm_build_1468 []byte, dm_build_1469 int) uint8 {
	return uint8(dm_build_1468[dm_build_1469] & 0xff)
}

func (Dm_build_1471 *dm_build_1345) Dm_build_1470(dm_build_1472 []byte, dm_build_1473 int) uint16 {
	var dm_build_1474 uint16
	dm_build_1474 = uint16(dm_build_1472[dm_build_1473] & 0xff)
	dm_build_1473++
	dm_build_1474 |= uint16(dm_build_1472[dm_build_1473]&0xff) << 8
	return dm_build_1474
}

func (Dm_build_1476 *dm_build_1345) Dm_build_1475(dm_build_1477 []byte, dm_build_1478 int) uint32 {
	var dm_build_1479 uint32
	dm_build_1479 = uint32(dm_build_1477[dm_build_1478] & 0xff)
	dm_build_1478++
	dm_build_1479 |= uint32(dm_build_1477[dm_build_1478]&0xff) << 8
	dm_build_1478++
	dm_build_1479 |= uint32(dm_build_1477[dm_build_1478]&0xff) << 16
	dm_build_1478++
	dm_build_1479 |= uint32(dm_build_1477[dm_build_1478]&0xff) << 24
	return dm_build_1479
}

func (Dm_build_1481 *dm_build_1345) Dm_build_1480(dm_build_1482 []byte, dm_build_1483 int) uint64 {
	var dm_build_1484 uint64
	dm_build_1484 = uint64(dm_build_1482[dm_build_1483] & 0xff)
	dm_build_1483++
	dm_build_1484 |= uint64(dm_build_1482[dm_build_1483]&0xff) << 8
	dm_build_1483++
	dm_build_1484 |= uint64(dm_build_1482[dm_build_1483]&0xff) << 16
	dm_build_1483++
	dm_build_1484 |= uint64(dm_build_1482[dm_build_1483]&0xff) << 24
	dm_build_1483++
	dm_build_1484 |= uint64(dm_build_1482[dm_build_1483]&0xff) << 32
	dm_build_1483++
	dm_build_1484 |= uint64(dm_build_1482[dm_build_1483]&0xff) << 40
	dm_build_1483++
	dm_build_1484 |= uint64(dm_build_1482[dm_build_1483]&0xff) << 48
	dm_build_1483++
	dm_build_1484 |= uint64(dm_build_1482[dm_build_1483]&0xff) << 56
	return dm_build_1484
}

func (Dm_build_1486 *dm_build_1345) Dm_build_1485(dm_build_1487 []byte, dm_build_1488 int) []byte {
	dm_build_1489 := Dm_build_1486.Dm_build_1475(dm_build_1487, dm_build_1488)

	dm_build_1490 := make([]byte, dm_build_1489)
	copy(dm_build_1490[:int(dm_build_1489)], dm_build_1487[dm_build_1488+4:dm_build_1488+4+int(dm_build_1489)])
	return dm_build_1490
}

func (Dm_build_1492 *dm_build_1345) Dm_build_1491(dm_build_1493 []byte, dm_build_1494 int) []byte {
	dm_build_1495 := Dm_build_1492.Dm_build_1470(dm_build_1493, dm_build_1494)

	dm_build_1496 := make([]byte, dm_build_1495)
	copy(dm_build_1496[:int(dm_build_1495)], dm_build_1493[dm_build_1494+2:dm_build_1494+2+int(dm_build_1495)])
	return dm_build_1496
}

func (Dm_build_1498 *dm_build_1345) Dm_build_1497(dm_build_1499 []byte, dm_build_1500 int, dm_build_1501 int) []byte {

	dm_build_1502 := make([]byte, dm_build_1501)
	copy(dm_build_1502[:dm_build_1501], dm_build_1499[dm_build_1500:dm_build_1500+dm_build_1501])
	return dm_build_1502
}

func (Dm_build_1504 *dm_build_1345) Dm_build_1503(dm_build_1505 []byte, dm_build_1506 int, dm_build_1507 int, dm_build_1508 string, dm_build_1509 *DmConnection) string {
	return Dm_build_1504.Dm_build_1598(dm_build_1505[dm_build_1506:dm_build_1506+dm_build_1507], dm_build_1508, dm_build_1509)
}

func (Dm_build_1511 *dm_build_1345) Dm_build_1510(dm_build_1512 []byte, dm_build_1513 int, dm_build_1514 string, dm_build_1515 *DmConnection) string {
	dm_build_1516 := Dm_build_1511.Dm_build_1475(dm_build_1512, dm_build_1513)
	dm_build_1513 += 4
	return Dm_build_1511.Dm_build_1503(dm_build_1512, dm_build_1513, int(dm_build_1516), dm_build_1514, dm_build_1515)
}

func (Dm_build_1518 *dm_build_1345) Dm_build_1517(dm_build_1519 []byte, dm_build_1520 int, dm_build_1521 string, dm_build_1522 *DmConnection) string {
	dm_build_1523 := Dm_build_1518.Dm_build_1470(dm_build_1519, dm_build_1520)
	dm_build_1520 += 2
	return Dm_build_1518.Dm_build_1503(dm_build_1519, dm_build_1520, int(dm_build_1523), dm_build_1521, dm_build_1522)
}

func (Dm_build_1525 *dm_build_1345) Dm_build_1524(dm_build_1526 byte) []byte {
	return []byte{dm_build_1526}
}

func (Dm_build_1528 *dm_build_1345) Dm_build_1527(dm_build_1529 int8) []byte {
	return []byte{byte(dm_build_1529)}
}

func (Dm_build_1531 *dm_build_1345) Dm_build_1530(dm_build_1532 int16) []byte {
	return []byte{byte(dm_build_1532), byte(dm_build_1532 >> 8)}
}

func (Dm_build_1534 *dm_build_1345) Dm_build_1533(dm_build_1535 int32) []byte {
	return []byte{byte(dm_build_1535), byte(dm_build_1535 >> 8), byte(dm_build_1535 >> 16), byte(dm_build_1535 >> 24)}
}

func (Dm_build_1537 *dm_build_1345) Dm_build_1536(dm_build_1538 int64) []byte {
	return []byte{byte(dm_build_1538), byte(dm_build_1538 >> 8), byte(dm_build_1538 >> 16), byte(dm_build_1538 >> 24), byte(dm_build_1538 >> 32),
		byte(dm_build_1538 >> 40), byte(dm_build_1538 >> 48), byte(dm_build_1538 >> 56)}
}

func (Dm_build_1540 *dm_build_1345) Dm_build_1539(dm_build_1541 float32) []byte {
	return Dm_build_1540.Dm_build_1551(math.Float32bits(dm_build_1541))
}

func (Dm_build_1543 *dm_build_1345) Dm_build_1542(dm_build_1544 float64) []byte {
	return Dm_build_1543.Dm_build_1554(math.Float64bits(dm_build_1544))
}

func (Dm_build_1546 *dm_build_1345) Dm_build_1545(dm_build_1547 uint8) []byte {
	return []byte{byte(dm_build_1547)}
}

func (Dm_build_1549 *dm_build_1345) Dm_build_1548(dm_build_1550 uint16) []byte {
	return []byte{byte(dm_build_1550), byte(dm_build_1550 >> 8)}
}

func (Dm_build_1552 *dm_build_1345) Dm_build_1551(dm_build_1553 uint32) []byte {
	return []byte{byte(dm_build_1553), byte(dm_build_1553 >> 8), byte(dm_build_1553 >> 16), byte(dm_build_1553 >> 24)}
}

func (Dm_build_1555 *dm_build_1345) Dm_build_1554(dm_build_1556 uint64) []byte {
	return []byte{byte(dm_build_1556), byte(dm_build_1556 >> 8), byte(dm_build_1556 >> 16), byte(dm_build_1556 >> 24), byte(dm_build_1556 >> 32), byte(dm_build_1556 >> 40), byte(dm_build_1556 >> 48), byte(dm_build_1556 >> 56)}
}

func (Dm_build_1558 *dm_build_1345) Dm_build_1557(dm_build_1559 []byte, dm_build_1560 string, dm_build_1561 *DmConnection) []byte {
	if dm_build_1560 == "UTF-8" {
		return dm_build_1559
	}

	if dm_build_1561 == nil {
		if e := dm_build_1603(dm_build_1560); e != nil {
			tmp, err := ioutil.ReadAll(
				transform.NewReader(bytes.NewReader(dm_build_1559), e.NewEncoder()),
			)
			if err != nil {
				panic("UTF8 To Charset error!")
			}

			return tmp
		}

		panic("Unsupported Charset!")
	}

	if dm_build_1561.encodeBuffer == nil {
		dm_build_1561.encodeBuffer = bytes.NewBuffer(nil)
		dm_build_1561.encode = dm_build_1603(dm_build_1561.getServerEncoding())
		dm_build_1561.transformReaderDst = make([]byte, 4096)
		dm_build_1561.transformReaderSrc = make([]byte, 4096)
	}

	if e := dm_build_1561.encode; e != nil {

		dm_build_1561.encodeBuffer.Reset()

		n, err := dm_build_1561.encodeBuffer.ReadFrom(
			Dm_build_1617(bytes.NewReader(dm_build_1559), e.NewEncoder(), dm_build_1561.transformReaderDst, dm_build_1561.transformReaderSrc),
		)
		if err != nil {
			panic("UTF8 To Charset error!")
		}
		var tmp = make([]byte, n)
		if _, err = dm_build_1561.encodeBuffer.Read(tmp); err != nil {
			panic("UTF8 To Charset error!")
		}
		return tmp
	}

	panic("Unsupported Charset!")
}

func (Dm_build_1563 *dm_build_1345) Dm_build_1562(dm_build_1564 string, dm_build_1565 string, dm_build_1566 *DmConnection) []byte {
	return Dm_build_1563.Dm_build_1557([]byte(dm_build_1564), dm_build_1565, dm_build_1566)
}

func (Dm_build_1568 *dm_build_1345) Dm_build_1567(dm_build_1569 []byte) byte {
	return Dm_build_1568.Dm_build_1439(dm_build_1569, 0)
}

func (Dm_build_1571 *dm_build_1345) Dm_build_1570(dm_build_1572 []byte) int16 {
	return Dm_build_1571.Dm_build_1443(dm_build_1572, 0)
}

func (Dm_build_1574 *dm_build_1345) Dm_build_1573(dm_build_1575 []byte) int32 {
	return Dm_build_1574.Dm_build_1448(dm_build_1575, 0)
}

func (Dm_build_1577 *dm_build_1345) Dm_build_1576(dm_build_1578 []byte) int64 {
	return Dm_build_1577.Dm_build_1453(dm_build_1578, 0)
}

func (Dm_build_1580 *dm_build_1345) Dm_build_1579(dm_build_1581 []byte) float32 {
	return Dm_build_1580.Dm_build_1458(dm_build_1581, 0)
}

func (Dm_build_1583 *dm_build_1345) Dm_build_1582(dm_build_1584 []byte) float64 {
	return Dm_build_1583.Dm_build_1462(dm_build_1584, 0)
}

func (Dm_build_1586 *dm_build_1345) Dm_build_1585(dm_build_1587 []byte) uint8 {
	return Dm_build_1586.Dm_build_1466(dm_build_1587, 0)
}

func (Dm_build_1589 *dm_build_1345) Dm_build_1588(dm_build_1590 []byte) uint16 {
	return Dm_build_1589.Dm_build_1470(dm_build_1590, 0)
}

func (Dm_build_1592 *dm_build_1345) Dm_build_1591(dm_build_1593 []byte) uint32 {
	return Dm_build_1592.Dm_build_1475(dm_build_1593, 0)
}

func (Dm_build_1595 *dm_build_1345) Dm_build_1594(dm_build_1596 []byte, dm_build_1597 string) []byte {
	if dm_build_1597 == "UTF-8" {
		return dm_build_1596
	}

	if e := dm_build_1603(dm_build_1597); e != nil {

		tmp, err := ioutil.ReadAll(
			transform.NewReader(bytes.NewReader(dm_build_1596), e.NewDecoder()),
		)
		if err != nil {

			panic("Charset To UTF8 error!")
		}

		return tmp
	}

	panic("Unsupported Charset!")

}

func (Dm_build_1599 *dm_build_1345) Dm_build_1598(dm_build_1600 []byte, dm_build_1601 string, dm_build_1602 *DmConnection) string {
	return string(Dm_build_1599.Dm_build_1594(dm_build_1600, dm_build_1601))
}

func dm_build_1603(dm_build_1604 string) encoding.Encoding {
	if e, err := ianaindex.MIB.Encoding(dm_build_1604); err == nil && e != nil {
		return e
	}
	return nil
}

type Dm_build_1605 struct {
	dm_build_1606 io.Reader
	dm_build_1607 transform.Transformer
	dm_build_1608 error

	dm_build_1609                []byte
	dm_build_1610, dm_build_1611 int

	dm_build_1612                []byte
	dm_build_1613, dm_build_1614 int

	dm_build_1615 bool
}

const dm_build_1616 = 4096

func Dm_build_1617(dm_build_1618 io.Reader, dm_build_1619 transform.Transformer, dm_build_1620 []byte, dm_build_1621 []byte) *Dm_build_1605 {
	dm_build_1619.Reset()
	return &Dm_build_1605{
		dm_build_1606: dm_build_1618,
		dm_build_1607: dm_build_1619,
		dm_build_1609: dm_build_1620,
		dm_build_1612: dm_build_1621,
	}
}

func (dm_build_1623 *Dm_build_1605) Read(dm_build_1624 []byte) (int, error) {
	dm_build_1625, dm_build_1626 := 0, error(nil)
	for {

		if dm_build_1623.dm_build_1610 != dm_build_1623.dm_build_1611 {
			dm_build_1625 = copy(dm_build_1624, dm_build_1623.dm_build_1609[dm_build_1623.dm_build_1610:dm_build_1623.dm_build_1611])
			dm_build_1623.dm_build_1610 += dm_build_1625
			if dm_build_1623.dm_build_1610 == dm_build_1623.dm_build_1611 && dm_build_1623.dm_build_1615 {
				return dm_build_1625, dm_build_1623.dm_build_1608
			}
			return dm_build_1625, nil
		} else if dm_build_1623.dm_build_1615 {
			return 0, dm_build_1623.dm_build_1608
		}

		if dm_build_1623.dm_build_1613 != dm_build_1623.dm_build_1614 || dm_build_1623.dm_build_1608 != nil {
			dm_build_1623.dm_build_1610 = 0
			dm_build_1623.dm_build_1611, dm_build_1625, dm_build_1626 = dm_build_1623.dm_build_1607.Transform(dm_build_1623.dm_build_1609, dm_build_1623.dm_build_1612[dm_build_1623.dm_build_1613:dm_build_1623.dm_build_1614], dm_build_1623.dm_build_1608 == io.EOF)
			dm_build_1623.dm_build_1613 += dm_build_1625

			switch {
			case dm_build_1626 == nil:
				if dm_build_1623.dm_build_1613 != dm_build_1623.dm_build_1614 {
					dm_build_1623.dm_build_1608 = nil
				}

				dm_build_1623.dm_build_1615 = dm_build_1623.dm_build_1608 != nil
				continue
			case dm_build_1626 == transform.ErrShortDst && (dm_build_1623.dm_build_1611 != 0 || dm_build_1625 != 0):

				continue
			case dm_build_1626 == transform.ErrShortSrc && dm_build_1623.dm_build_1614-dm_build_1623.dm_build_1613 != len(dm_build_1623.dm_build_1612) && dm_build_1623.dm_build_1608 == nil:

			default:
				dm_build_1623.dm_build_1615 = true

				if dm_build_1623.dm_build_1608 == nil || dm_build_1623.dm_build_1608 == io.EOF {
					dm_build_1623.dm_build_1608 = dm_build_1626
				}
				continue
			}
		}

		if dm_build_1623.dm_build_1613 != 0 {
			dm_build_1623.dm_build_1613, dm_build_1623.dm_build_1614 = 0, copy(dm_build_1623.dm_build_1612, dm_build_1623.dm_build_1612[dm_build_1623.dm_build_1613:dm_build_1623.dm_build_1614])
		}
		dm_build_1625, dm_build_1623.dm_build_1608 = dm_build_1623.dm_build_1606.Read(dm_build_1623.dm_build_1612[dm_build_1623.dm_build_1614:])
		dm_build_1623.dm_build_1614 += dm_build_1625
	}
}
