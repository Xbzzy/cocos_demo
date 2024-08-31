import {useState} from 'react';

function Square({value, onClickSquare}) {
    return (
        <button className="square" onClick={onClickSquare}>
            {value}
        </button>
    );
}

export default function Board() {
    const [isNext, setIsNext] = useState(true);
    const [square, setSquares] = useState(Array(9).fill(null));

    const [count, setCount] = useState(0);
    const [records, setRecords] = useState(Array(9).fill(null));

    let winner = CheckWinner(square);
    let status;
    if (winner) {
        status = winner + " has win!";
    } else {
        status = 'Next player: ' + (isNext ? 'X' : 'O');
    }

    function handleClick(index) {
        if (winner || square[index]) {
            return;
        }

        const nextSquare = square.slice();

        if (isNext) {
            nextSquare[index] = "X";
        } else {
            nextSquare[index] = "O";
        }

        // 添加记录
        const nextRecords = records.slice();
        nextRecords[count] = index;
        setRecords(nextRecords);
        setCount(count + 1);

        console.log('click ' + records, count);

        setSquares(nextSquare);
        setIsNext(!isNext);
    }

    function handleBackLast() {
        const nextSquare = square.slice();

        nextSquare[records[count - 1]] = null
        setCount(count - 1);

        console.log('cancel' + records, count);

        setSquares(nextSquare);
        setIsNext(!isNext);
    }

    return (
        <>
            <div className="status">{status}</div>
            <div className="board-row">
                <Square value={square[0]} onClickSquare={() => handleClick(0)}/>
                <Square value={square[1]} onClickSquare={() => handleClick(1)}/>
                <Square value={square[2]} onClickSquare={() => handleClick(2)}/>
            </div>
            <div className="board-row">
                <Square value={square[3]} onClickSquare={() => handleClick(3)}/>
                <Square value={square[4]} onClickSquare={() => handleClick(4)}/>
                <Square value={square[5]} onClickSquare={() => handleClick(5)}/>
            </div>
            <div className="board-row">
                <Square value={square[6]} onClickSquare={() => handleClick(6)}/>
                <Square value={square[7]} onClickSquare={() => handleClick(7)}/>
                <Square value={square[8]} onClickSquare={() => handleClick(8)}/>
            </div>
            <div>
                <button onClick={handleBackLast}>back to last time</button>
            </div>
        </>
    );
}

function CheckWinner(squares) {
    const lines = [
        [0, 1, 2],
        [3, 4, 5],
        [6, 7, 8],
        [0, 3, 6],
        [1, 4, 7],
        [2, 5, 8],
        [0, 4, 8],
        [2, 4, 6]
    ];
    for (let i = 0; i < lines.length; i++) {
        const [a, b, c] = lines[i];
        if (squares[a] && squares[a] === squares[b] && squares[a] === squares[c]) {
            return squares[a];
        }
    }
    return null;
}
